package handler

import (
	"io"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/racio/orvion/pkg"
)

// asyncMirrorWriter 把上游响应旁路给日志记录 pipe 时使用。
//
// 原实现里 io.Pipe 写端同步阻塞:RecordLog goroutine 读得慢、或 io.Pipe
// 的 32KiB 缓冲被吃满,都会反压到上游读链,导致上游 TCP 连接长期占用。
// 这里把 pw 写入转到内部 goroutine,并提供一个固定容量的 chunk 缓冲;
// 当缓冲满时 Write 不阻塞、直接丢弃,保证上游读链可以继续推进。
//
// 丢弃的代价:RecordLog 看到的字节流可能缺段,token 统计会落到 usage 估算兜底。
// 比让整条请求僵死要好得多。
type asyncMirrorWriter struct {
	pw        *io.PipeWriter
	ch        chan []byte
	done      chan struct{}
	logID     uint
	logUUID   string
	dropped   atomic.Int64
	closeOnce sync.Once
	errOnce   sync.Once
	err       error
}

func newAsyncMirrorWriter(pw *io.PipeWriter, bufChunks int, logID uint, logUUID string) *asyncMirrorWriter {
	if bufChunks <= 0 {
		bufChunks = 64
	}
	w := &asyncMirrorWriter{
		pw:      pw,
		ch:      make(chan []byte, bufChunks),
		done:    make(chan struct{}),
		logID:   logID,
		logUUID: logUUID,
	}
	pkg.GoSafe("handler.async_mirror_writer", w.run)
	return w
}

func (w *asyncMirrorWriter) run() {
	defer close(w.done)
	var writeErr error
	for chunk := range w.ch {
		if writeErr != nil {
			continue
		}
		if _, err := w.pw.Write(chunk); err != nil {
			writeErr = err
		}
	}
	switch {
	case writeErr != nil:
		_ = w.pw.CloseWithError(writeErr)
	case w.err != nil:
		_ = w.pw.CloseWithError(w.err)
	default:
		_ = w.pw.Close()
	}
}

// Write 永不阻塞;满时丢弃当前 chunk。返回的 n 始终等于 len(p),避免
// MultiWriter/TeeReader 因"写端"失败而中止整条数据流。
func (w *asyncMirrorWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// MultiWriter 会复用调用方 buffer,这里必须拷贝。
	buf := make([]byte, len(p))
	copy(buf, p)
	select {
	case w.ch <- buf:
	default:
		w.dropped.Add(int64(len(p)))
	}
	return len(p), nil
}

func (w *asyncMirrorWriter) Close() error {
	w.closeOnce.Do(func() { close(w.ch) })
	<-w.done
	if dropped := w.Dropped(); dropped > 0 {
		slog.Warn("响应日志镜像发生数据丢弃，日志与用量估算可能不完整", "log_id", w.logID, "log_uuid", w.logUUID, "dropped_bytes", dropped)
	}
	return nil
}

func (w *asyncMirrorWriter) CloseWithError(err error) error {
	w.errOnce.Do(func() { w.err = err })
	return w.Close()
}

// Dropped 返回累计被丢弃的字节数。
func (w *asyncMirrorWriter) Dropped() int64 {
	return w.dropped.Load()
}
