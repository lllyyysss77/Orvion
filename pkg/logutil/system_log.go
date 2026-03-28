package logutil

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

const defaultSystemLogFileName = "orvion.log"

func ResolveSystemLogFilePath() string {
	rawPath := strings.TrimSpace(os.Getenv("LOG_FILE"))
	if rawPath == "" {
		rawPath = defaultSystemLogFileName
	}

	if filepath.IsAbs(rawPath) {
		return rawPath
	}

	absPath, err := filepath.Abs(rawPath)
	if err != nil {
		return rawPath
	}
	return absPath
}

func OpenSystemLogFile() (*os.File, error) {
	path := ResolveSystemLogFilePath()
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

func NewSystemLogWriter(fallback io.Writer) io.Writer {
	logFile, err := OpenSystemLogFile()
	if err != nil {
		return fallback
	}

	if fallback == nil {
		return logFile
	}

	return io.MultiWriter(fallback, logFile)
}

func ClearSystemLogFile() error {
	path := ResolveSystemLogFilePath()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return file.Close()
}
