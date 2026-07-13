package handler

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// responseSpool 将上游响应暂存到仅当前用户可读写的临时文件。
// 它不依赖日志消费者的处理速度，因此不会因内存队列满而丢弃响应片段。
type responseSpool struct {
	file     *os.File
	path     string
	writeErr error
}

func newResponseSpool() (*responseSpool, error) {
	file, err := os.CreateTemp("", "orvion-response-*.spool")
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	return &responseSpool{file: file, path: file.Name()}, nil
}

func (s *responseSpool) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if s == nil || s.file == nil {
		return 0, errors.New("response spool is not initialized")
	}
	if s.writeErr != nil {
		// 捕获失败后不再影响客户端响应链；最终 Reader 会明确返回截断错误。
		return len(p), nil
	}
	n, err := s.file.Write(p)
	if err != nil {
		s.writeErr = err
	} else if n != len(p) {
		s.writeErr = io.ErrShortWrite
	}
	// 日志捕获失败不能中断已经成功的上游响应。
	return len(p), nil
}

func (s *responseSpool) Reader(terminalErr error) (io.ReadCloser, error) {
	if s == nil || s.file == nil {
		return nil, errors.New("response spool is not initialized")
	}
	if s.writeErr != nil {
		err := fmt.Errorf("response spool truncated: %w", s.writeErr)
		s.cleanup()
		return nil, err
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		s.cleanup()
		return nil, err
	}
	reader := &responseSpoolReader{
		file:        s.file,
		path:        s.path,
		terminalErr: terminalErr,
	}
	s.file = nil
	return reader, nil
}

func (s *responseSpool) cleanup() {
	if s == nil {
		return
	}
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
	if s.path != "" {
		_ = os.Remove(s.path)
	}
}

type responseSpoolReader struct {
	file                 *os.File
	path                 string
	terminalErr          error
	terminalErrDelivered bool
}

func (r *responseSpoolReader) Read(p []byte) (int, error) {
	n, err := r.file.Read(p)
	if !errors.Is(err, io.EOF) || r.terminalErr == nil || r.terminalErrDelivered {
		return n, err
	}
	if n > 0 {
		return n, nil
	}
	r.terminalErrDelivered = true
	return 0, r.terminalErr
}

func (r *responseSpoolReader) Close() error {
	closeErr := r.file.Close()
	removeErr := os.Remove(r.path)
	if closeErr != nil {
		return closeErr
	}
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return removeErr
	}
	return nil
}
