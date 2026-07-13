package handler

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestResponseSpoolPreservesAllFragmentsAndRemovesFile(t *testing.T) {
	spool, err := newResponseSpool()
	if err != nil {
		t.Fatalf("创建响应暂存文件失败: %v", err)
	}
	path := spool.path
	for _, fragment := range []string{"data: first\n", "data: second\n", "data: usage\n"} {
		if _, err := spool.Write([]byte(fragment)); err != nil {
			t.Fatalf("写入响应片段失败: %v", err)
		}
	}

	reader, err := spool.Reader(nil)
	if err != nil {
		t.Fatalf("打开响应暂存读取器失败: %v", err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("读取响应暂存内容失败: %v", err)
	}
	if got, want := string(raw), "data: first\ndata: second\ndata: usage\n"; got != want {
		t.Fatalf("响应暂存内容不完整: got=%q want=%q", got, want)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("关闭响应暂存读取器失败: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("响应暂存文件应在读取完成后删除: %v", err)
	}
}

func TestResponseSpoolReturnsTerminalStreamErrorAfterCapturedData(t *testing.T) {
	spool, err := newResponseSpool()
	if err != nil {
		t.Fatalf("创建响应暂存文件失败: %v", err)
	}
	if _, err := spool.Write([]byte("partial response")); err != nil {
		t.Fatalf("写入响应片段失败: %v", err)
	}
	wantErr := errors.New("upstream interrupted")
	reader, err := spool.Reader(wantErr)
	if err != nil {
		t.Fatalf("打开响应暂存读取器失败: %v", err)
	}
	defer reader.Close()

	var output strings.Builder
	_, err = io.Copy(&output, reader)
	if !errors.Is(err, wantErr) {
		t.Fatalf("应在已捕获内容后返回上游错误: %v", err)
	}
	if output.String() != "partial response" {
		t.Fatalf("上游错误前的响应内容不完整: %q", output.String())
	}
}
