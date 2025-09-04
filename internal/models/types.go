package models

import (
	"fmt"
	"io"
	"strings"
	"time"
)

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
			return fmt.Sprintf("%d msgs, %s", li.Count, HumanBytes(li.Size))
		}
		if li.Size > 0 {
			return fmt.Sprintf("~%s", HumanBytes(li.Size))
		}
		return "computing..."
	}
	return HumanBytes(li.Size)
}

func (li ListItem) FilterValue() string { return li.Title() }

type MsgEntry struct {
	UID       uint32
	Subject   string
	From      string
	Date      time.Time
	SizeBytes uint64
	Mailbox   string // mailbox name for list mode
}

// simpleLiteral implements imap.Literal interface
type SimpleLiteral struct {
	data []byte
	pos  int
}

func NewLiteral(data []byte) *SimpleLiteral {
	return &SimpleLiteral{data: data}
}

func (l *SimpleLiteral) Read(p []byte) (int, error) {
	if l.pos >= len(l.data) {
		return 0, io.EOF
	}
	n := copy(p, l.data[l.pos:])
	l.pos += n
	return n, nil
}

func (l *SimpleLiteral) Len() int {
	return len(l.data)
}

func Truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n-1]) + "…"
}

func HumanBytes(b uint64) string {
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

func PathString(p []string) string {
	if len(p) == 0 {
		return "/"
	}
	return "/" + strings.Join(p, "/")
}
