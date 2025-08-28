package imap

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-message"

	"maildu/internal/cache"
	"maildu/internal/models"
)

const (
	// How many messages to fetch sizes in parallel per mailbox
	parallelFetch = 8
)

func FetchMailboxes(ctx context.Context, ic *Conn) (map[string]*models.MailboxInfo, error) {
	log.Printf("Starting fetchMailboxes...")
	mboxes := map[string]*models.MailboxInfo{}
	ch := make(chan *imap.MailboxInfo, 50)
	done := make(chan error, 1)

	client := ic.GetClient()
	if client == nil {
		return nil, fmt.Errorf("no valid IMAP client")
	}

	go func() {
		log.Printf("Calling IMAP List command...")
		done <- client.List("", "*", ch)
	}()

	count := 0
	for m := range ch {
		mb := &models.MailboxInfo{Name: m.Name}
		mboxes[m.Name] = mb
		count++
	}

	if err := <-done; err != nil {
		log.Printf("List command failed: %v", err)
		return nil, err
	}

	// Fetch exists counts quickly by STATUS
	statusCount := 0
	for name := range mboxes {
		status, err := client.Status(name, []imap.StatusItem{imap.StatusMessages})
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

func detectHierarchySep(m map[string]*models.MailboxInfo) string {
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

func RootLevel(m map[string]*models.MailboxInfo) []*models.MailboxInfo {
	var roots []*models.MailboxInfo
	for _, mb := range m {
		if mb.Parent == nil {
			roots = append(roots, mb)
		}
	}
	return roots
}

func ComputeMailboxSizes(ctx context.Context, ic *Conn, cache *cache.Cache, boxes map[string]*models.MailboxInfo) error {
	processed := 0
	for name := range boxes {
		size, err := MailboxApproxSize(ctx, ic, cache, name)
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

func MailboxApproxSize(ctx context.Context, ic *Conn, cache *cache.Cache, mailbox string) (uint64, error) {
	// Ensure we have a valid authenticated connection before proceeding
	if err := ic.ensureAuthenticated(ctx); err != nil {
		return 0, fmt.Errorf("failed to ensure IMAP authentication: %w", err)
	}

	client := ic.GetClient()
	if client == nil {
		return 0, fmt.Errorf("no valid IMAP client")
	}

	_, err := client.Select(mailbox, true)
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

	if err := client.UidFetch(seqset, items, ch); err != nil {
		return 0, err
	}
	wg.Wait()
	if fetchErr != nil {
		return 0, fetchErr
	}
	return total, nil
}

func FetchMessagesApprox(ctx context.Context, ic *Conn, cache *cache.Cache, mailbox string) ([]*models.MsgEntry, error) {
	// Ensure we have a valid authenticated connection before proceeding
	if err := ic.ensureAuthenticated(ctx); err != nil {
		return nil, fmt.Errorf("failed to ensure IMAP authentication: %w", err)
	}

	client := ic.GetClient()
	if client == nil {
		return nil, fmt.Errorf("no valid IMAP client")
	}

	mbox, err := client.Select(mailbox, true)
	if err != nil {
		return nil, err
	}
	if mbox.Messages == 0 {
		return []*models.MsgEntry{}, nil
	}
	seqset := new(imap.SeqSet)
	seqset.AddRange(1, 0) // 0 represents * (all messages)
	items := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchRFC822Size, imap.FetchInternalDate}
	ch := make(chan *imap.Message, 200)

	msgs := make([]*models.MsgEntry, 0, mbox.Messages)
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
				me := &models.MsgEntry{
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

	if err := client.UidFetch(seqset, items, ch); err != nil {
		return nil, err
	}
	wg.Wait()
	if fetchErr != nil {
		return nil, fetchErr
	}
	return msgs, nil
}

func DeleteMessage(ctx context.Context, ic *Conn, mailbox string, uid uint32, dryRun bool) error {
	if dryRun {
		return nil
	}

	// Ensure we have a valid authenticated connection before proceeding
	if err := ic.ensureAuthenticated(ctx); err != nil {
		return fmt.Errorf("failed to ensure IMAP authentication: %w", err)
	}

	client := ic.GetClient()
	if client == nil {
		return fmt.Errorf("no valid IMAP client")
	}

	_, err := client.Select(mailbox, false)
	if err != nil {
		return err
	}
	seq := new(imap.SeqSet)
	seq.AddNum(uid)
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	flags := []interface{}{imap.DeletedFlag}
	if err := client.UidStore(seq, item, flags, nil); err != nil {
		return err
	}
	return client.Expunge(nil)
}

// stripAttachments downloads the message, rebuilds it removing attachments (Content-Disposition: attachment),
// keeps inline parts, re-uploads preserving flags/date, then deletes original.
func StripAttachments(ctx context.Context, ic *Conn, mailbox string, uid uint32, dryRun bool) error {
	// Ensure we have a valid authenticated connection before proceeding
	if err := ic.ensureAuthenticated(ctx); err != nil {
		return fmt.Errorf("failed to ensure IMAP authentication: %w", err)
	}

	client := ic.GetClient()
	if client == nil {
		return fmt.Errorf("no valid IMAP client")
	}

	_, err := client.Select(mailbox, false)
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
	if err := client.UidFetch(seq, items, ch); err != nil {
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

	// append and set flags/date
	if err := client.Append(mailbox, origFlags, msg.InternalDate, newMsg); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	// delete original
	del := new(imap.SeqSet)
	del.AddNum(uid)
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	if err := client.UidStore(del, item, []interface{}{imap.DeletedFlag}, nil); err != nil {
		return err
	}
	return client.Expunge(nil)
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
		return models.NewLiteral([]byte(b.String())), nil
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
	return models.NewLiteral([]byte(b.String())), nil
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
