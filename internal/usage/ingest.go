package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"yardmaster/internal/store"
)

const (
	settingLogOffset = "routing_log.offset"
	// The routing log is truncated once it's this big and fully read, but only while
	// switchyard-server is stopped: it keeps the file open for appending, so truncating
	// underneath it at any other time could lose a record.
	rotateSize = 32 << 20
)

// Ingester tails Switchyard's routing log into the ledger.
type Ingester struct {
	path   string
	db     *store.DB
	ledger *Ledger
	mu     sync.Mutex // one reader at a time
}

func NewIngester(path string, db *store.DB, ledger *Ledger) *Ingester {
	return &Ingester{path: path, db: db, ledger: ledger}
}

// Run reads new records every second until ctx ends.
func (in *Ingester) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := in.ReadNew(ctx); err != nil {
				slog.Error("reading the routing log", "err", err)
			}
		}
	}
}

// ReadNew reads complete records appended since the last call.
func (in *Ingester) ReadNew(ctx context.Context) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	f, err := os.Open(in.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	var offset int64
	if _, err := in.db.GetSetting(ctx, settingLogOffset, &offset); err != nil {
		return err
	}
	if info.Size() < offset {
		// The file was truncated or replaced: start over.
		offset = 0
	}
	if info.Size() == offset {
		return nil
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break // a partial last line is read next time, once it's complete
		}
		if err != nil {
			return err
		}
		lineOffset := offset
		offset += int64(len(line))
		var rec Record
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		id := rec.TrialID
		if id == "" {
			// Not sent through the gateway (e.g. the playground): its position in the log
			// is a stable ID, so re-reading after a crash doesn't double count.
			id = fmt.Sprintf("log-%s-%d", rec.TS, lineOffset)
		}
		in.ledger.AddRecord(id, rec)
	}
	return in.db.SetSetting(ctx, settingLogOffset, offset)
}

// RotateIfLarge truncates the routing log once it's large and fully read. Call it only
// while switchyard-server is stopped.
func (in *Ingester) RotateIfLarge(ctx context.Context) {
	if err := in.ReadNew(ctx); err != nil {
		slog.Error("reading the routing log before rotating", "err", err)
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	info, err := os.Stat(in.path)
	if err != nil || info.Size() < rotateSize {
		return
	}
	var offset int64
	in.db.GetSetting(ctx, settingLogOffset, &offset)
	if offset < info.Size() {
		return
	}
	if err := os.Truncate(in.path, 0); err != nil {
		slog.Error("rotating the routing log", "err", err)
		return
	}
	in.db.SetSetting(ctx, settingLogOffset, int64(0))
	slog.Info("routing log rotated", "size", info.Size())
}
