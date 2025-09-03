package cache

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	cacheBucketMsgs = "msgmeta"
	cacheBucketMbox = "mboxmeta"
	cacheVersionKey = "cache_version"
	cacheVersion    = uint32(1)
)

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

func Open(path string) (*Cache, error) {
	log.Printf("Creating cache directory for path: %s", path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("Failed to create cache directory: %v", err)
		return nil, err
	}

	log.Printf("Opening BoltDB at: %s", path)
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 30 * time.Second})
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
