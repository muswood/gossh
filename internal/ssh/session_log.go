// owner: muswood | Email: mumu920@outlook.com
package ssh

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const sessionLogDirectoryName = ".gossh"

// SessionLog appends the bytes visibly delivered to one terminal session.
// Callers own deciding which transport bytes are visible terminal content.
type SessionLog struct {
	mu     sync.Mutex
	file   *os.File
	path   string
	closed bool
}

func defaultSessionLogRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户目录: %w", err)
	}
	base := filepath.Join(home, sessionLogDirectoryName)
	if err := os.MkdirAll(base, 0700); err != nil {
		return "", fmt.Errorf("创建终端日志根目录失败: %w", err)
	}
	if err := os.Chmod(base, 0700); err != nil {
		return "", fmt.Errorf("设置终端日志根目录权限失败: %w", err)
	}
	return filepath.Join(base, "logs"), nil
}

func openSessionLog(root, sessionID string, createdAt time.Time) (*SessionLog, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, fmt.Errorf("创建终端日志目录失败: %w", err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		return nil, fmt.Errorf("设置终端日志目录权限失败: %w", err)
	}
	directory := filepath.Join(root, createdAt.Format("2006-01-02"))
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("创建终端日志目录失败: %w", err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, fmt.Errorf("设置终端日志目录权限失败: %w", err)
	}

	name := safeSessionLogName(sessionID)
	path := filepath.Join(directory, name+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("打开终端日志失败: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("设置终端日志权限失败: %w", err)
	}
	return &SessionLog{file: file, path: path}, nil
}

func safeSessionLogName(sessionID string) string {
	name := filepath.Base(strings.ReplaceAll(sessionID, "\\", "/"))
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "session"
	}
	return name
}

func (l *SessionLog) Write(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return fmt.Errorf("终端日志已关闭")
	}
	if _, err := l.file.Write(data); err != nil {
		return fmt.Errorf("写入终端日志失败: %w", err)
	}
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("刷新终端日志失败: %w", err)
	}
	return nil
}

func (l *SessionLog) Read(offset int64, maxBytes int) ([]byte, int64, bool, error) {
	if offset < 0 {
		return nil, 0, false, fmt.Errorf("终端日志偏移不能为负数")
	}
	if maxBytes <= 0 || maxBytes > 64*1024 {
		return nil, 0, false, fmt.Errorf("终端日志单次读取大小必须在 1 到 65536 字节之间")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return nil, 0, false, fmt.Errorf("终端日志已关闭")
	}
	info, err := l.file.Stat()
	if err != nil {
		return nil, 0, false, fmt.Errorf("读取终端日志状态失败: %w", err)
	}
	if offset > info.Size() {
		offset = info.Size()
	}
	data := make([]byte, maxBytes)
	n, readErr := l.file.ReadAt(data, offset)
	if readErr != nil && readErr != io.EOF {
		return nil, offset, false, fmt.Errorf("读取终端日志失败: %w", readErr)
	}
	next := offset + int64(n)
	return data[:n], next, next >= info.Size(), nil
}

func (l *SessionLog) Size() (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.file == nil {
		return 0, fmt.Errorf("终端日志已关闭")
	}
	info, err := l.file.Stat()
	if err != nil {
		return 0, fmt.Errorf("读取终端日志状态失败: %w", err)
	}
	return info.Size(), nil
}

func (l *SessionLog) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.file == nil {
		return nil
	}
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("关闭终端日志失败: %w", err)
	}
	l.file = nil
	return nil
}
