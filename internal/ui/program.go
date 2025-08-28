package ui

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"maildu/internal/cache"
	"maildu/internal/imap"
	"maildu/internal/models"
)

type Program struct {
	cfg   imap.Config
	cache *cache.Cache
	ic    *imap.Conn
}

func NewProgram(cfg imap.Config, cache *cache.Cache, ic *imap.Conn) *tea.Program {
	model := initialModel(cfg, cache, ic)
	return tea.NewProgram(model, tea.WithAltScreen())
}

type model struct {
	cfg   imap.Config
	cache *cache.Cache
	ic    *imap.Conn

	help help.Model
	keys keyMap
	list list.Model

	// navigation
	currentMailbox string // empty -> root mailboxes
	path           []string

	// data
	mailboxes map[string]*models.MailboxInfo
	loadedMsg map[string][]*models.MsgEntry // mailbox -> messages
	sortBy    string                        // "size" or "name"

	// loading state
	loading    bool
	loadingMsg string

	err error
}

func initialModel(cfg imap.Config, cache *cache.Cache, ic *imap.Conn) model {
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
		mailboxes:      map[string]*models.MailboxInfo{},
		loadedMsg:      map[string][]*models.MsgEntry{},
		sortBy:         "size",
		loading:        true,
		loadingMsg:     "Connecting to IMAP server...",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.loadMailboxesCmd(), tea.EnterAltScreen)
}

func (m model) loadMailboxesCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		boxes, err := imap.FetchMailboxes(ctx, m.ic)
		if err != nil {
			log.Printf("Error fetching mailboxes: %v", err)
			return errMsg{err}
		}

		// Compute sizes (approx via RFC822.SIZE aggregate)
		if err := imap.ComputeMailboxSizes(ctx, m.ic, m.cache, boxes); err != nil {
			log.Printf("Error computing mailbox sizes: %v", err)
			return errMsg{err}
		}

		return boxesMsg{boxes}
	}
}

type errMsg struct{ error }
type boxesMsg struct {
	boxes map[string]*models.MailboxInfo
}
type msgsMsg struct {
	mailbox string
	msgs    []*models.MsgEntry
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
		m.loading = false
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
	header := fmt.Sprintf("Server: %s  Mailbox: %s  Sort: %s%s\n", m.cfg.Server, models.PathString(m.path), m.sortBy, dryRunStatus)
	if m.err != nil {
		header += lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("Error: "+m.err.Error()) + "\n"
	}

	if m.loading {
		loadingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
		header += loadingStyle.Render("⏳ "+m.loadingMsg) + "\n"
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
		roots := imap.RootLevel(m.mailboxes)
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
			items = append(items, models.ListItem{
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
		title := fmt.Sprintf("[%d] %s — %s", me.UID, models.Truncate(me.Subject, 80), me.From)
		items = append(items, models.ListItem{
			Name:    title,
			Message: me,
			Size:    me.SizeBytes,
		})
	}
	m.list.Title = m.currentMailbox
	m.list.SetItems(items)
	return nil
}

func (m model) handleEnter() (tea.Model, tea.Cmd) {
	sel, ok := m.list.SelectedItem().(models.ListItem)
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
		msgs, err := imap.FetchMessagesApprox(ctx, m.ic, m.cache, mailbox)
		if err != nil {
			return errMsg{err}
		}
		return msgsMsg{mailbox: mailbox, msgs: msgs}
	}
}

func (m model) handleDelete() (tea.Model, tea.Cmd) {
	sel, ok := m.list.SelectedItem().(models.ListItem)
	if !ok || sel.Message == nil || m.currentMailbox == "" {
		return m, nil
	}
	uid := sel.Message.UID
	mb := m.currentMailbox
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := imap.DeleteMessage(ctx, m.ic, mb, uid, m.cfg.DryRun); err != nil {
			return errMsg{err}
		}
		// refresh mailbox
		msgs, err := imap.FetchMessagesApprox(ctx, m.ic, m.cache, mb)
		if err != nil {
			return errMsg{err}
		}
		return msgsMsg{mailbox: mb, msgs: msgs}
	}
}

func (m model) handleStrip() (tea.Model, tea.Cmd) {
	sel, ok := m.list.SelectedItem().(models.ListItem)
	if !ok || sel.Message == nil || m.currentMailbox == "" {
		return m, nil
	}
	uid := sel.Message.UID
	mb := m.currentMailbox
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := imap.StripAttachments(ctx, m.ic, mb, uid, m.cfg.DryRun); err != nil {
			return errMsg{err}
		}
		msgs, err := imap.FetchMessagesApprox(ctx, m.ic, m.cache, mb)
		if err != nil {
			return errMsg{err}
		}
		return msgsMsg{mailbox: mb, msgs: msgs}
	}
}
