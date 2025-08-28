package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message"
	"github.com/joho/godotenv"
	bolt "go.etcd.io/bbolt"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func init() {
	// Load .env file if it exists
	if err := godotenv.Load(); err != nil {
		// Don't fail if .env file doesn't exist, just continue
		log.Printf("No .env file found or error loading it: %v", err)
	}
}

// simpleLiteral implements imap.Literal interface
type simpleLiteral struct {
	data []byte
	pos  int
}

func newLiteral(data []byte) imap.Literal {
	return &simpleLiteral{data: data}
}

func (l *simpleLiteral) Read(p []byte) (int, error) {
	if l.pos >= len(l.data) {
		return 0, io.EOF
	}
	n := copy(p, l.data[l.pos:])
	l.pos += n
	return n, nil
}

func (l *simpleLiteral) Len() int {
	return len(l.data)
}

const (
	appName          = "imapdu"
	defaultCacheFile = "imapdu-cache.bolt"
	cacheBucketMsgs  = "msgmeta"
	cacheBucketMbox  = "mboxmeta"
	cacheVersionKey  = "cache_version"
	cacheVersion     = uint32(1)

	// How many messages to fetch sizes in parallel per mailbox
	parallelFetch = 8
	// How long to wait between IDLE refreshes or periodic refresh
	refreshInterval = 3 * time.Minute
)

type Config struct {
	Server   string
	Port     int
	TLS      bool
	Username string
	Password string
	Cache    string
	Timeout  time.Duration
	SkipCert bool
	DryRun   bool
}

func loadConfig() (Config, error) {
	var cfg Config
	flag.StringVar(&cfg.Server, "server", os.Getenv("IMAP_SERVER"), "IMAP server host (env IMAP_SERVER)")
	flag.IntVar(&cfg.Port, "port", envInt("IMAP_PORT", 993), "IMAP server port (env IMAP_PORT)")
	flag.BoolVar(&cfg.TLS, "tls", envBool("IMAP_TLS", true), "Use TLS")
	flag.BoolVar(&cfg.SkipCert, "skip-cert-verify", envBool("IMAP_SKIP_CERT_VERIFY", false), "Skip TLS certificate verification")
	flag.StringVar(&cfg.Username, "user", os.Getenv("IMAP_USER"), "IMAP username (env IMAP_USER)")
	flag.StringVar(&cfg.Password, "pass", os.Getenv("IMAP_PASS"), "IMAP password (env IMAP_PASS)")
	flag.StringVar(&cfg.Cache, "cache", envStr("IMAPDU_CACHE", filepath.Join(defaultDataDir(), defaultCacheFile)), "Cache file path")
	flag.DurationVar(&cfg.Timeout, "timeout", envDuration("IMAP_TIMEOUT", 30*time.Second), "Network timeout")
	flag.BoolVar(&cfg.DryRun, "dry-run", envBool("IMAPDU_DRY_RUN", true), "Dry run mode - prevent actual deletion of mails/attachments (env IMAPDU_DRY_RUN)")
	flag.Parse()

	if cfg.Server == "" || cfg.Username == "" || cfg.Password == "" {
		return cfg, fmt.Errorf("missing server/user/pass; set flags or env IMAP_SERVER/IMAP_USER/IMAP_PASS")
	}
	return cfg, nil
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		var i int
		fmt.Sscanf(v, "%d", &i)
		if i != 0 {
			return i
		}
	}
	return def
}
func envBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "y":
			return true
		case "0", "false", "no", "n":
			return false
		}
	}
	return def
}
func envStr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func envDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
func defaultDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, appName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", appName)
}

type Cache struct {
	db *bolt.DB
}

type MsgMeta struct {
	UID        uint32
	MailboxID  string // stable mailbox key (e.g., name)
	ApproxSize uint64 // bytes (IMAP RFC822.SIZE or heuristic)
	ModSeq     uint64 // optional if CONDSTORE enabled
	Internal   int64  // Internal date unix
}

func openCache(path string) (*Cache, error) {
	log.Printf("Creating cache directory for path: %s", path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("Failed to create cache directory: %v", err)
		return nil, err
	}

	log.Printf("Opening BoltDB at: %s", path)
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		log.Printf("Failed to open BoltDB: %v", err)
		return nil, err
	}

	log.Printf("Initializing cache buckets...")
	err = db.Update(func(tx *bolt.Tx) error {
		if _, e := tx.CreateBucketIfNotExists([]byte(cacheBucketMsgs)); e != nil {
			return e
		}
		if _, e := tx.CreateBucketIfNotExists([]byte(cacheBucketMbox)); e != nil {
			return e
		}
		b := tx.Bucket([]byte(cacheBucketMbox))
		if cur := b.Get([]byte(cacheVersionKey)); cur == nil {
			buf := make([]byte, 4)
			binary.BigEndian.PutUint32(buf, cacheVersion)
			return b.Put([]byte(cacheVersionKey), buf)
		}
		return nil
	})
	if err != nil {
		log.Printf("Failed to initialize cache buckets: %v", err)
		_ = db.Close()
		return nil, err
	}

	log.Printf("Cache opened successfully")
	return &Cache{db: db}, nil
}

func (c *Cache) Close() error { return c.db.Close() }

func cacheKey(mailbox string, uid uint32) []byte {
	return []byte(fmt.Sprintf("%s|%08x", mailbox, uid))
}

func (c *Cache) GetMsgSize(mailbox string, uid uint32) (uint64, bool) {
	var sz uint64
	ok := false
	_ = c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(cacheBucketMsgs))
		v := b.Get(cacheKey(mailbox, uid))
		if len(v) == 8 {
			sz = binary.BigEndian.Uint64(v)
			ok = true
		}
		return nil
	})
	return sz, ok
}

func (c *Cache) PutMsgSize(mailbox string, uid uint32, size uint64) error {
	return c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(cacheBucketMsgs))
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, size)
		return b.Put(cacheKey(mailbox, uid), buf)
	})
}

type IMAPConn struct {
	cl     *client.Client
	cfg    Config
	mu     sync.Mutex
	authed bool
}

func dialIMAP(ctx context.Context, cfg Config) (*IMAPConn, error) {
	dialer := &net.Dialer{Timeout: cfg.Timeout}
	var c *client.Client
	var err error
	if cfg.TLS {
		tlsCfg := &tls.Config{
			ServerName:         cfg.Server,
			InsecureSkipVerify: cfg.SkipCert, // if true, user accepts risk
		}
		c, err = client.DialWithDialerTLS(dialer, fmt.Sprintf("%s:%d", cfg.Server, cfg.Port), tlsCfg)
	} else {
		c, err = client.DialWithDialer(dialer, fmt.Sprintf("%s:%d", cfg.Server, cfg.Port))
	}
	if err != nil {
		return nil, err
	}
	// Login
	if err = c.Login(cfg.Username, cfg.Password); err != nil {
		_ = c.Logout()
		return nil, err
	}
	return &IMAPConn{cl: c, cfg: cfg, authed: true}, nil
}

func (ic *IMAPConn) Logout() error {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if ic.cl != nil {
		return ic.cl.Logout()
	}
	return nil
}

type MailboxInfo struct {
	Name     string
	Exists   uint32
	SizeSum  uint64
	Children []*MailboxInfo
	Parent   *MailboxInfo
}

type ListItem struct {
	Name      string
	IsMailbox bool
	Mailbox   *MailboxInfo
	Message   *MsgEntry
	Size      uint64
	Count     uint32
}

func (li ListItem) Title() string {
	if li.IsMailbox {
		return fmt.Sprintf("%s/", li.Name)
	}
	return li.Name
}
func (li ListItem) Description() string {
	if li.IsMailbox {
		if li.Count > 0 {
			return fmt.Sprintf("%d msgs, %s", li.Count, humanBytes(li.Size))
		}
		return "empty"
	}
	return humanBytes(li.Size)
}
func (li ListItem) FilterValue() string { return li.Title() }

type MsgEntry struct {
	UID       uint32
	Subject   string
	From      string
	Date      time.Time
	SizeBytes uint64
}

type keyMap struct {
	Up, Down, Enter, Back, Delete, Strip, Refresh, Quit key.Binding
	ToggleSort                                          key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Enter:   key.NewBinding(key.WithKeys("enter", "l"), key.WithHelp("enter/l", "open")),
		Back:    key.NewBinding(key.WithKeys("h", "backspace"), key.WithHelp("h/⌫", "back")),
		Delete:  key.NewBinding(key.WithKeys("x", "delete"), key.WithHelp("x", "delete msg")),
		Strip:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "strip attach")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		ToggleSort: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "toggle sort"),
		),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Back, k.Delete, k.Strip, k.Refresh, k.Quit, k.ToggleSort}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Enter, k.Back},
		{k.Delete, k.Strip, k.Refresh, k.Quit, k.ToggleSort},
	}
}

type model struct {
	cfg   Config
	cache *Cache
	ic    *IMAPConn

	help help.Model
	keys keyMap
	list list.Model

	// navigation
	currentMailbox string // empty -> root mailboxes
	path           []string

	// data
	mailboxes map[string]*MailboxInfo
	loadedMsg map[string][]*MsgEntry // mailbox -> messages
	sortBy    string                 // "size" or "name"

	err error
}

func initialModel(cfg Config, cache *Cache, ic *IMAPConn) model {
	ls := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	ls.Title = "imapdu"
	ls.SetShowStatusBar(false)
	ls.SetFilteringEnabled(true)
	ls.DisableQuitKeybindings()
	ls.SetShowHelp(false)
	return model{
		cfg:            cfg,
		cache:          cache,
		ic:             ic,
		help:           help.New(),
		keys:           newKeyMap(),
		list:           ls,
		currentMailbox: "",
		path:           []string{},
		mailboxes:      map[string]*MailboxInfo{},
		loadedMsg:      map[string][]*MsgEntry{},
		sortBy:         "size",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadMailboxesCmd(), tea.EnterAltScreen)
}

func (m model) loadMailboxesCmd() tea.Cmd {
	return func() tea.Msg {
		log.Printf("Starting to load mailboxes...")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		log.Printf("Fetching mailboxes from IMAP server...")
		boxes, err := fetchMailboxes(ctx, m.ic)
		if err != nil {
			log.Printf("Error fetching mailboxes: %v", err)
			return errMsg{err}
		}

		// Compute sizes (approx via RFC822.SIZE aggregate)
		log.Printf("Computing mailbox sizes...")
		if err := computeMailboxSizes(ctx, m.ic, m.cache, boxes); err != nil {
			log.Printf("Error computing mailbox sizes: %v", err)
			return errMsg{err}
		}

		return boxesMsg{boxes}
	}
}

type errMsg struct{ error }
type boxesMsg struct{ boxes map[string]*MailboxInfo }
type msgsMsg struct {
	mailbox string
	msgs    []*MsgEntry
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.list.SetSize(msg.Width, msg.Height-2)
		return m, nil
	case errMsg:
		m.err = msg.error
		return m, nil
	case boxesMsg:
		m.mailboxes = msg.boxes
		return m, m.refreshListCmd()
	case msgsMsg:
		m.loadedMsg[msg.mailbox] = msg.msgs
		// If still in that mailbox, refresh view
		if m.currentMailbox == msg.mailbox {
			return m, m.refreshListCmd()
		}
		return m, nil
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Up):
			m.list.CursorUp()
		case key.Matches(msg, m.keys.Down):
			m.list.CursorDown()
		case key.Matches(msg, m.keys.Enter):
			return m.handleEnter()
		case key.Matches(msg, m.keys.Back):
			return m.handleBack()
		case key.Matches(msg, m.keys.Refresh):
			return m, m.refreshCmd()
		case key.Matches(msg, m.keys.Delete):
			return m.handleDelete()
		case key.Matches(msg, m.keys.Strip):
			return m.handleStrip()
		case key.Matches(msg, m.keys.ToggleSort):
			if m.sortBy == "size" {
				m.sortBy = "name"
			} else {
				m.sortBy = "size"
			}
			return m, m.refreshListCmd()
		}
		// pass to list
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	default:
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
}

func (m model) View() string {
	dryRunStatus := ""
	if m.cfg.DryRun {
		dryRunStatus = " [DRY RUN]"
	}
	header := fmt.Sprintf("Server: %s  Mailbox: %s  Sort: %s%s\n", m.cfg.Server, pathString(m.path), m.sortBy, dryRunStatus)
	if m.err != nil {
		header += lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("Error: "+m.err.Error()) + "\n"
	}
	return header + m.list.View() + "\n" + m.help.View(m.keys)
}

func (m model) refreshCmd() tea.Cmd {
	if m.currentMailbox == "" {
		return m.loadMailboxesCmd()
	}
	return m.loadMessagesCmd(m.currentMailbox)
}

func (m model) refreshListCmd() tea.Cmd {
	if m.currentMailbox == "" {
		// show mailboxes
		items := []list.Item{}
		roots := rootLevel(m.mailboxes)
		sort.Slice(roots, func(i, j int) bool {
			if m.sortBy == "size" {
				if roots[i].SizeSum == roots[j].SizeSum {
					return roots[i].Name < roots[j].Name
				}
				return roots[i].SizeSum > roots[j].SizeSum
			}
			return roots[i].Name < roots[j].Name
		})
		for _, mb := range roots {
			items = append(items, ListItem{
				Name:      mb.Name,
				IsMailbox: true,
				Mailbox:   mb,
				Size:      mb.SizeSum,
				Count:     mb.Exists,
			})
		}
		m.list.Title = "Mailboxes"
		m.list.SetItems(items)
		return nil
	}
	// messages inside mailbox
	msgs := m.loadedMsg[m.currentMailbox]
	if msgs == nil {
		return m.loadMessagesCmd(m.currentMailbox)
	}
	sort.Slice(msgs, func(i, j int) bool {
		if m.sortBy == "size" {
			if msgs[i].SizeBytes == msgs[j].SizeBytes {
				return msgs[i].Date.After(msgs[j].Date)
			}
			return msgs[i].SizeBytes > msgs[j].SizeBytes
		}
		// name = subject
		return strings.ToLower(msgs[i].Subject) < strings.ToLower(msgs[j].Subject)
	})
	items := make([]list.Item, 0, len(msgs))
	for _, me := range msgs {
		title := fmt.Sprintf("[%d] %s — %s", me.UID, truncate(me.Subject, 80), me.From)
		items = append(items, ListItem{
			Name:    title,
			Message: me,
			Size:    me.SizeBytes,
		})
	}
	m.list.Title = m.currentMailbox
	m.list.SetItems(items)
	return nil
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n-1]) + "…"
}

func (m model) handleEnter() (tea.Model, tea.Cmd) {
	sel, ok := m.list.SelectedItem().(ListItem)
	if !ok {
		return m, nil
	}
	if sel.IsMailbox {
		// drill down: if has children, go down. If leaf, open messages
		m.path = append(m.path, sel.Mailbox.Name)
		m.currentMailbox = sel.Mailbox.Name
		return m, m.loadMessagesCmd(sel.Mailbox.Name)
	}
	// For messages, maybe later show detail; for now no-op
	return m, nil
}

func (m model) handleBack() (tea.Model, tea.Cmd) {
	if len(m.path) == 0 {
		return m, nil
	}
	m.path = m.path[:len(m.path)-1]
	if len(m.path) == 0 {
		m.currentMailbox = ""
		return m, m.refreshListCmd()
	}
	m.currentMailbox = m.path[len(m.path)-1]
	return m, m.refreshListCmd()
}

func (m model) loadMessagesCmd(mailbox string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		msgs, err := fetchMessagesApprox(ctx, m.ic, m.cache, mailbox)
		if err != nil {
			return errMsg{err}
		}
		return msgsMsg{mailbox: mailbox, msgs: msgs}
	}
}

func (m model) handleDelete() (tea.Model, tea.Cmd) {
	sel, ok := m.list.SelectedItem().(ListItem)
	if !ok || sel.Message == nil || m.currentMailbox == "" {
		return m, nil
	}
	uid := sel.Message.UID
	mb := m.currentMailbox
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := deleteMessage(ctx, m.ic, mb, uid, m.cfg.DryRun); err != nil {
			return errMsg{err}
		}
		// refresh mailbox
		msgs, err := fetchMessagesApprox(ctx, m.ic, m.cache, mb)
		if err != nil {
			return errMsg{err}
		}
		return msgsMsg{mailbox: mb, msgs: msgs}
	}
}

func (m model) handleStrip() (tea.Model, tea.Cmd) {
	sel, ok := m.list.SelectedItem().(ListItem)
	if !ok || sel.Message == nil || m.currentMailbox == "" {
		return m, nil
	}
	uid := sel.Message.UID
	mb := m.currentMailbox
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := stripAttachments(ctx, m.ic, mb, uid, m.cfg.DryRun); err != nil {
			return errMsg{err}
		}
		msgs, err := fetchMessagesApprox(ctx, m.ic, m.cache, mb)
		if err != nil {
			return errMsg{err}
		}
		return msgsMsg{mailbox: mb, msgs: msgs}
	}
}

func pathString(p []string) string {
	if len(p) == 0 {
		return "/"
	}
	return "/" + strings.Join(p, "/")
}

// IMAP helpers

func fetchMailboxes(ctx context.Context, ic *IMAPConn) (map[string]*MailboxInfo, error) {
	log.Printf("Starting fetchMailboxes...")
	mboxes := map[string]*MailboxInfo{}
	ch := make(chan *imap.MailboxInfo, 50)
	done := make(chan error, 1)
	go func() {
		log.Printf("Calling IMAP List command...")
		done <- ic.cl.List("", "*", ch)
	}()

	count := 0
	for m := range ch {
		mb := &MailboxInfo{Name: m.Name}
		mboxes[m.Name] = mb
		count++
		count++
	}

	if err := <-done; err != nil {
		log.Printf("List command failed: %v", err)
		return nil, err
	}

	// Fetch exists counts quickly by STATUS
	statusCount := 0
	for name := range mboxes {
		status, err := ic.cl.Status(name, []imap.StatusItem{imap.StatusMessages})
		if err != nil {
			log.Printf("Status failed for mailbox %s: %v", name, err)
			continue
		}
		mboxes[name].Exists = status.Messages
		statusCount++

	}

	// hierarchy
	sep := detectHierarchySep(mboxes)
	for name, mb := range mboxes {
		if idx := strings.LastIndex(name, sep); idx >= 0 {
			parentName := name[:idx]
			if parent, ok := mboxes[parentName]; ok {
				mb.Parent = parent
				parent.Children = append(parent.Children, mb)
			}
		}
	}
	return mboxes, nil
}

func detectHierarchySep(m map[string]*MailboxInfo) string {
	// Try common separators
	seps := []string{"/", "."}
	for _, s := range seps {
		for name := range m {
			if strings.Contains(name, s) {
				return s
			}
		}
	}
	return "/"
}

func rootLevel(m map[string]*MailboxInfo) []*MailboxInfo {
	var roots []*MailboxInfo
	for _, mb := range m {
		if mb.Parent == nil {
			roots = append(roots, mb)
		}
	}
	// Sort by size desc later in refresh
	return roots
}

func computeMailboxSizes(ctx context.Context, ic *IMAPConn, cache *Cache, boxes map[string]*MailboxInfo) error {
	processed := 0
	for name := range boxes {
		size, err := mailboxApproxSize(ctx, ic, cache, name)
		if err != nil {
			log.Printf("Error computing size for mailbox %s: %v", name, err)
			// best effort
			continue
		}
		boxes[name].SizeSum = size
		processed++
	}
	return nil
}

func mailboxApproxSize(ctx context.Context, ic *IMAPConn, cache *Cache, mailbox string) (uint64, error) {
	_, err := ic.cl.Select(mailbox, true)
	if err != nil {
		return 0, err
	}
	seqset := new(imap.SeqSet)
	seqset.AddRange(1, 0) // 0 represents * (all messages)
	items := []imap.FetchItem{imap.FetchUid, imap.FetchRFC822Size, imap.FetchInternalDate}

	ch := make(chan *imap.Message, 100)
	var mu sync.Mutex
	var total uint64
	var wg sync.WaitGroup
	work := make(chan *imap.Message, 100)

	// workers to add sizes and cache
	for i := 0; i < parallelFetch; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for msg := range work {
				uid := msg.Uid
				var sz uint64
				if cached, ok := cache.GetMsgSize(mailbox, uid); ok && cached > 0 {
					sz = cached
				} else {
					// Approx size from RFC822.SIZE (already bytes on wire)
					if msg.Size > 0 {
						sz = uint64(msg.Size)
					} else {
						// fallback estimate
						sz = 1024
					}
					_ = cache.PutMsgSize(mailbox, uid, sz)
				}
				mu.Lock()
				total += sz
				mu.Unlock()
			}
		}()
	}

	var fetchErr error
	go func() {
		defer close(work)
		for msg := range ch {
			select {
			case <-ctx.Done():
				fetchErr = ctx.Err()
				return
			default:
				work <- msg
			}
		}
	}()

	if err := ic.cl.UidFetch(seqset, items, ch); err != nil {
		return 0, err
	}
	wg.Wait()
	if fetchErr != nil {
		return 0, fetchErr
	}
	return total, nil
}

func fetchMessagesApprox(ctx context.Context, ic *IMAPConn, cache *Cache, mailbox string) ([]*MsgEntry, error) {
	mbox, err := ic.cl.Select(mailbox, true)
	if err != nil {
		return nil, err
	}
	if mbox.Messages == 0 {
		return []*MsgEntry{}, nil
	}
	seqset := new(imap.SeqSet)
	seqset.AddRange(1, 0) // 0 represents * (all messages)
	items := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchRFC822Size, imap.FetchInternalDate}
	ch := make(chan *imap.Message, 200)

	msgs := make([]*MsgEntry, 0, mbox.Messages)
	var mu sync.Mutex
	var wg sync.WaitGroup
	work := make(chan *imap.Message, 200)

	for i := 0; i < parallelFetch; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for msg := range work {
				if msg == nil {
					continue
				}
				uid := msg.Uid
				sub := ""
				from := ""
				date := time.Now()
				if msg.Envelope != nil {
					sub = msg.Envelope.Subject
					if len(msg.Envelope.From) > 0 {
						a := msg.Envelope.From[0]
						from = fmt.Sprintf("%s %s <%s@%s>", a.PersonalName, a.PersonalName, a.MailboxName, a.HostName)
						from = strings.TrimSpace(strings.ReplaceAll(from, "  ", " "))
					}
					if !msg.Envelope.Date.IsZero() {
						date = msg.Envelope.Date
					}
				}
				var size uint64
				if cached, ok := cache.GetMsgSize(mailbox, uid); ok && cached > 0 {
					size = cached
				} else if msg.Size > 0 {
					size = uint64(msg.Size)
					_ = cache.PutMsgSize(mailbox, uid, size)
				} else {
					size = 1024
				}
				me := &MsgEntry{
					UID:       uid,
					Subject:   sub,
					From:      from,
					Date:      date,
					SizeBytes: size,
				}
				mu.Lock()
				msgs = append(msgs, me)
				mu.Unlock()
			}
		}()
	}

	var fetchErr error
	go func() {
		defer close(work)
		for m := range ch {
			select {
			case <-ctx.Done():
				fetchErr = ctx.Err()
				return
			default:
				work <- m
			}
		}
	}()

	if err := ic.cl.UidFetch(seqset, items, ch); err != nil {
		return nil, err
	}
	wg.Wait()
	if fetchErr != nil {
		return nil, fetchErr
	}
	return msgs, nil
}

func deleteMessage(ctx context.Context, ic *IMAPConn, mailbox string, uid uint32, dryRun bool) error {
	if dryRun {
		return nil
	}
	_, err := ic.cl.Select(mailbox, false)
	if err != nil {
		return err
	}
	seq := new(imap.SeqSet)
	seq.AddNum(uid)
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	flags := []interface{}{imap.DeletedFlag}
	if err := ic.cl.UidStore(seq, item, flags, nil); err != nil {
		return err
	}
	return ic.cl.Expunge(nil)
}

// stripAttachments downloads the message, rebuilds it removing attachments (Content-Disposition: attachment),
// keeps inline parts, re-uploads preserving flags/date, then deletes original.
func stripAttachments(ctx context.Context, ic *IMAPConn, mailbox string, uid uint32, dryRun bool) error {
	_, err := ic.cl.Select(mailbox, false)
	if err != nil {
		return err
	}
	origFlags := []string{}
	// fetch full message
	seq := new(imap.SeqSet)
	seq.AddNum(uid)
	section := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchInternalDate, section.FetchItem()}
	ch := make(chan *imap.Message, 1)
	if err := ic.cl.UidFetch(seq, items, ch); err != nil {
		return err
	}
	var msg *imap.Message
	for m := range ch {
		msg = m
	}
	if msg == nil {
		return fmt.Errorf("message not found")
	}
	if len(msg.Flags) > 0 {
		origFlags = append(origFlags, msg.Flags...)
	}
	r := msg.GetBody(section)
	if r == nil {
		return fmt.Errorf("cannot get message body")
	}

	// parse and rebuild without attachments
	newMsg, err := buildMessageWithoutAttachments(r)
	if err != nil {
		return err
	}

	// append and set flags/date - fix the function call to remove extra parameter
	if err := ic.cl.Append(mailbox, origFlags, msg.InternalDate, newMsg); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	// delete original
	del := new(imap.SeqSet)
	del.AddNum(uid)
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	if err := ic.cl.UidStore(del, item, []interface{}{imap.DeletedFlag}, nil); err != nil {
		return err
	}
	return ic.cl.Expunge(nil)
}

func buildMessageWithoutAttachments(r imap.Literal) (imap.Literal, error) {
	mr, err := message.Read(r)
	if err != nil {
		return nil, err
	}
	// If not multipart, return as is
	mediaType, _, _ := mr.Header.ContentType()
	if !strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		// nothing to strip - just return original content as new literal
		var b strings.Builder
		w, _ := message.CreateWriter(&b, mr.Header)
		if _, err := w.Write([]byte("Attachment stripping not applicable.\n")); err != nil {
			return nil, err
		}
		_ = w.Close()
		return newLiteral([]byte(b.String())), nil
	}

	var b strings.Builder
	w, err := message.CreateWriter(&b, mr.Header)
	if err != nil {
		return nil, err
	}

	multi := mr.MultipartReader()
	for {
		p, perr := multi.NextPart()
		if perr != nil {
			break
		}
		disposition, _, _ := p.Header.ContentDisposition()
		if strings.EqualFold(disposition, "attachment") {
			// skip attachments
			continue
		}
		// copy inline parts
		pw, err := w.CreatePart(p.Header)
		if err != nil {
			return nil, err
		}
		if _, err := copyPart(pw, p.Body); err != nil {
			return nil, err
		}
	}
	_ = w.Close()
	return newLiteral([]byte(b.String())), nil
}

func copyPart(dst interface{ Write([]byte) (int, error) }, src interface{ Read([]byte) (int, error) }) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			w, werr := dst.Write(buf[:n])
			total += int64(w)
			if werr != nil {
				return total, werr
			}
		}
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return total, nil
			}
			// io.EOF or others
			return total, err
		}
	}
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func main() {
	log.Printf("Starting imapdu application...")

	log.Printf("Loading configuration...")
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Configuration loaded successfully. Server: %s, User: %s", cfg.Server, cfg.Username)

	log.Printf("Opening cache at: %s", cfg.Cache)
	cache, err := openCache(cfg.Cache)
	if err != nil {
		log.Fatal(err)
	}
	defer cache.Close()
	log.Printf("Cache opened successfully")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("Connecting to IMAP server %s:%d...", cfg.Server, cfg.Port)
	ic, err := dialIMAP(ctx, cfg)
	if err != nil {
		log.Printf("Failed to connect to IMAP server: %v", err)
		log.Fatal(err)
	}
	defer ic.Logout()

	log.Printf("Starting TUI...")
	p := tea.NewProgram(initialModel(cfg, cache, ic), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
