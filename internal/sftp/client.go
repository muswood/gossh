// owner: muswood | Email: mumu920@outlook.com
package sftp

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const maxTransferAttempts = 3

var errChecksumMismatch = errors.New("SHA-256 校验失败")

const (
	extensionPosixRename = "posix-rename@openssh.com"
	extensionStatVFS     = "statvfs@openssh.com"
	extensionFSync       = "fsync@openssh.com"
)

type FileInfo struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	IsDir      bool   `json:"isDir"`
	IsSymlink  bool   `json:"isSymlink"`
	LinkTarget string `json:"linkTarget,omitempty"`
	Perm       string `json:"perm"`
	ModTime    string `json:"modTime"`
}

type TransferProgress struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"` // upload / download
	FileName string  `json:"fileName"`
	Total    int64   `json:"total"`
	Done     int64   `json:"done"`
	Percent  float64 `json:"percent"`
	Status   string  `json:"status"` // running / completed / failed
	Attempt  int     `json:"attempt"`
	Resumed  bool    `json:"resumed"`
	Verified bool    `json:"verified"`
}

type Extensions struct {
	PosixRename bool `json:"posixRename"`
	StatVFS     bool `json:"statVfs"`
	FSync       bool `json:"fsync"`
}

type DiskUsage struct {
	Path           string `json:"path"`
	BlockSize      uint64 `json:"blockSize"`
	TotalBytes     uint64 `json:"totalBytes"`
	FreeBytes      uint64 `json:"freeBytes"`
	AvailableBytes uint64 `json:"availableBytes"`
	Files          uint64 `json:"files"`
	FreeFiles      uint64 `json:"freeFiles"`
	NameMax        uint64 `json:"nameMax"`
}

type ReadRangeResult struct {
	Content       string `json:"content"`
	StartLine     int    `json:"startLine"`
	EndLine       int    `json:"endLine"`
	ReturnedLines int    `json:"returnedLines"`
	NextStartLine int    `json:"nextStartLine,omitempty"`
	HasMore       bool   `json:"hasMore"`
}

type Client struct {
	sshClient  *ssh.Client
	jumpClient *ssh.Client
	authCloser io.Closer
	ownsSSH    bool
	sftp       *sftp.Client
	mu         sync.Mutex
}

func NewClient(sshClient *ssh.Client) *Client {
	return &Client{sshClient: sshClient}
}

// NewDedicatedClient owns the SSH transport and closes it along with SFTP.
func NewDedicatedClient(sshClient, jumpClient *ssh.Client, authCloser io.Closer) *Client {
	return &Client{sshClient: sshClient, jumpClient: jumpClient, authCloser: authCloser, ownsSSH: true}
}

func (c *Client) connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sftp != nil {
		return nil
	}
	sc, err := sftp.NewClient(c.sshClient)
	if err != nil {
		if strings.Contains(err.Error(), "rejected: connect failed") {
			return fmt.Errorf("远端 SSH 服务拒绝 SFTP 子系统；请确认服务器已启用 SFTP: %w", err)
		}
		return fmt.Errorf("创建 SFTP 客户端失败: %w", err)
	}
	c.sftp = sc
	return nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var firstErr error
	if c.sftp != nil {
		if err := c.sftp.Close(); err != nil {
			firstErr = err
		}
		c.sftp = nil
	}
	if c.ownsSSH && c.sshClient != nil {
		if err := c.sshClient.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		c.sshClient = nil
	}
	if c.ownsSSH && c.jumpClient != nil {
		if err := c.jumpClient.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		c.jumpClient = nil
	}
	if c.authCloser != nil {
		if err := c.authCloser.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		c.authCloser = nil
	}
	return firstErr
}

func (c *Client) ListDir(remotePath string) ([]FileInfo, error) {
	if err := c.connect(); err != nil {
		return nil, err
	}
	if remotePath == "" {
		wd, err := c.sftp.Getwd()
		if err != nil {
			return nil, err
		}
		remotePath = wd
	}

	entries, err := c.sftp.ReadDir(remotePath)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败: %w", err)
	}

	files := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		entryPath := pathpkg.Join(remotePath, entry.Name())
		linkTarget := ""
		isSymlink := entry.Mode()&os.ModeSymlink != 0
		if isSymlink {
			linkTarget, _ = c.sftp.ReadLink(entryPath)
		}
		files = append(files, FileInfo{
			Name:       entry.Name(),
			Path:       entryPath,
			Size:       entry.Size(),
			IsDir:      entry.IsDir(),
			IsSymlink:  isSymlink,
			LinkTarget: linkTarget,
			Perm:       entry.Mode().String(),
			ModTime:    entry.ModTime().Format("2006-01-02 15:04"),
		})
	}
	return files, nil
}

func (c *Client) Mkdir(remotePath string) error {
	if err := c.connect(); err != nil {
		return err
	}
	return c.sftp.MkdirAll(remotePath)
}

func (c *Client) Remove(remotePath string) error {
	if err := c.connect(); err != nil {
		return err
	}
	info, err := c.sftp.Lstat(remotePath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return c.sftp.RemoveDirectory(remotePath)
	}
	return c.sftp.Remove(remotePath)
}

func (c *Client) RemoveRecursive(remotePath string) error {
	if err := c.connect(); err != nil {
		return err
	}
	info, err := c.sftp.Lstat(remotePath)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return c.sftp.Remove(remotePath)
	}
	entries, err := c.sftp.ReadDir(remotePath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		child := pathpkg.Join(remotePath, entry.Name())
		if err := c.RemoveRecursive(child); err != nil {
			return err
		}
	}
	return c.sftp.RemoveDirectory(remotePath)
}

func (c *Client) Chmod(remotePath string, mode os.FileMode) error {
	if err := c.connect(); err != nil {
		return err
	}
	return c.sftp.Chmod(remotePath, mode)
}

func (c *Client) Symlink(targetPath, linkPath string) error {
	if err := c.connect(); err != nil {
		return err
	}
	return c.sftp.Symlink(targetPath, linkPath)
}

func (c *Client) Rename(oldPath, newPath string) error {
	if err := c.connect(); err != nil {
		return err
	}
	return c.sftp.Rename(oldPath, newPath)
}

func (c *Client) Stat(remotePath string) (*FileInfo, error) {
	if err := c.connect(); err != nil {
		return nil, err
	}
	info, err := c.sftp.Stat(remotePath)
	if err != nil {
		return nil, err
	}
	return &FileInfo{
		Name:      info.Name(),
		Path:      remotePath,
		Size:      info.Size(),
		IsDir:     info.IsDir(),
		IsSymlink: info.Mode()&os.ModeSymlink != 0,
		Perm:      info.Mode().String(),
		ModTime:   info.ModTime().Format("2006-01-02 15:04"),
	}, nil
}

func (c *Client) Extensions() (Extensions, error) {
	if err := c.connect(); err != nil {
		return Extensions{}, err
	}
	_, posixRename := c.sftp.HasExtension(extensionPosixRename)
	_, statVFS := c.sftp.HasExtension(extensionStatVFS)
	_, fsync := c.sftp.HasExtension(extensionFSync)
	return Extensions{PosixRename: posixRename, StatVFS: statVFS, FSync: fsync}, nil
}

func (c *Client) RealPath(remotePath string) (string, error) {
	if err := c.connect(); err != nil {
		return "", err
	}
	return c.sftp.RealPath(remotePath)
}

func (c *Client) DiskUsage(remotePath string) (*DiskUsage, error) {
	if err := c.connect(); err != nil {
		return nil, err
	}
	stat, err := c.sftp.StatVFS(remotePath)
	if err != nil {
		return nil, fmt.Errorf("读取远端文件系统信息失败: %w", err)
	}
	return &DiskUsage{
		Path: remotePath, BlockSize: stat.Frsize,
		TotalBytes: stat.TotalSpace(), FreeBytes: stat.FreeSpace(), AvailableBytes: stat.Frsize * stat.Bavail,
		Files: stat.Files, FreeFiles: stat.Ffree, NameMax: stat.Namemax,
	}, nil
}

func (c *Client) Upload(localPath, remotePath string, progressChan chan<- TransferProgress) error {
	return c.UploadContext(context.Background(), localPath, remotePath, progressChan)
}

func (c *Client) UploadContext(ctx context.Context, localPath, remotePath string, progressChan chan<- TransferProgress) error {
	for attempt := 1; attempt <= maxTransferAttempts; attempt++ {
		err := c.uploadSmartOnce(ctx, localPath, remotePath, progressChan, attempt)
		if err == nil || errors.Is(err, context.Canceled) {
			return err
		}
		if errors.Is(err, errChecksumMismatch) {
			_ = c.removeQuiet(uploadTempPath(remotePath))
		}
		if attempt == maxTransferAttempts {
			return fmt.Errorf("上传失败，已重试 %d 次: %w", maxTransferAttempts, err)
		}
		c.report(progressChan, TransferProgress{Type: "upload", FileName: filepath.Base(localPath), Status: "retrying", Attempt: attempt + 1})
		_ = c.Close()
	}
	return nil
}

func (c *Client) uploadSmartOnce(ctx context.Context, localPath, remotePath string, progressChan chan<- TransferProgress, attempt int) error {
	if err := c.connect(); err != nil {
		return err
	}
	if _, ok := c.sftp.HasExtension(extensionPosixRename); ok {
		return c.uploadAtomicOnce(ctx, localPath, remotePath, progressChan, attempt)
	}
	return c.uploadToPathOnce(ctx, localPath, remotePath, progressChan, attempt)
}

func (c *Client) uploadAtomicOnce(ctx context.Context, localPath, remotePath string, progressChan chan<- TransferProgress, attempt int) error {
	tempPath := uploadTempPath(remotePath)
	if err := c.uploadToPathOnce(ctx, localPath, tempPath, progressChan, attempt); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.renameAtomic(tempPath, remotePath); err != nil {
		return err
	}
	remoteDigest, err := c.digestRemote(ctx, remotePath)
	if err != nil {
		return err
	}
	localDigest, err := digestLocal(ctx, localPath)
	if err != nil {
		return err
	}
	if localDigest != remoteDigest {
		return errChecksumMismatch
	}
	return nil
}

func (c *Client) uploadOnce(ctx context.Context, localPath, remotePath string, progressChan chan<- TransferProgress, attempt int) error {
	return c.uploadToPathOnce(ctx, localPath, remotePath, progressChan, attempt)
}

func (c *Client) uploadToPathOnce(ctx context.Context, localPath, remotePath string, progressChan chan<- TransferProgress, attempt int) error {
	if err := c.connect(); err != nil {
		return err
	}

	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("打开本地文件失败: %w", err)
	}
	defer localFile.Close()

	stat, err := localFile.Stat()
	if err != nil {
		return err
	}

	if err := c.sftp.MkdirAll(pathpkg.Dir(remotePath)); err != nil {
		return fmt.Errorf("创建远程目录失败: %w", err)
	}

	var offset int64
	if remoteInfo, statErr := c.sftp.Stat(remotePath); statErr == nil && !remoteInfo.IsDir() {
		offset = remoteInfo.Size()
		if offset > stat.Size() {
			offset = 0
		}
	}
	remoteFile, err := c.sftp.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE)
	if err != nil {
		return fmt.Errorf("创建远程文件失败: %w", err)
	}
	defer remoteFile.Close()
	if offset == 0 {
		if err := remoteFile.Truncate(0); err != nil {
			return fmt.Errorf("截断远程文件失败: %w", err)
		}
	} else if _, err := localFile.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("定位本地断点失败: %w", err)
	}
	if _, err := remoteFile.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("定位远程断点失败: %w", err)
	}

	buf := make([]byte, 64*1024)
	written := offset
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, err := localFile.Read(buf)
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 {
			break
		}
		if _, err := remoteFile.Write(buf[:n]); err != nil {
			return err
		}
		written += int64(n)
		c.report(progressChan, transferProgress("upload", localPath, stat.Size(), written, "running", attempt, offset > 0, false))
	}
	if err := remoteFile.Close(); err != nil {
		return err
	}
	c.report(progressChan, transferProgress("upload", localPath, stat.Size(), written, "verifying", attempt, offset > 0, false))
	localDigest, err := digestLocal(ctx, localPath)
	if err != nil {
		return err
	}
	remoteDigest, err := c.digestRemote(ctx, remotePath)
	if err != nil {
		return err
	}
	if localDigest != remoteDigest {
		return errChecksumMismatch
	}
	if err := c.syncRemoteFile(remotePath); err != nil {
		return err
	}
	c.report(progressChan, transferProgress("upload", localPath, stat.Size(), written, "running", attempt, offset > 0, true))
	return nil
}

func (c *Client) Download(remotePath, localPath string, progressChan chan<- TransferProgress) error {
	return c.DownloadContext(context.Background(), remotePath, localPath, progressChan)
}

func (c *Client) DownloadContext(ctx context.Context, remotePath, localPath string, progressChan chan<- TransferProgress) error {
	for attempt := 1; attempt <= maxTransferAttempts; attempt++ {
		err := c.downloadOnce(ctx, remotePath, localPath, progressChan, attempt)
		if err == nil || errors.Is(err, context.Canceled) {
			return err
		}
		if errors.Is(err, errChecksumMismatch) {
			_ = os.Remove(localPath)
		}
		if attempt == maxTransferAttempts {
			return fmt.Errorf("下载失败，已重试 %d 次: %w", maxTransferAttempts, err)
		}
		c.report(progressChan, TransferProgress{Type: "download", FileName: pathpkg.Base(remotePath), Status: "retrying", Attempt: attempt + 1})
		_ = c.Close()
	}
	return nil
}

func (c *Client) downloadOnce(ctx context.Context, remotePath, localPath string, progressChan chan<- TransferProgress, attempt int) error {
	if err := c.connect(); err != nil {
		return err
	}

	stat, err := c.sftp.Stat(remotePath)
	if err != nil {
		return fmt.Errorf("获取远程文件信息失败: %w", err)
	}

	remoteFile, err := c.sftp.Open(remotePath)
	if err != nil {
		return fmt.Errorf("打开远程文件失败: %w", err)
	}
	defer remoteFile.Close()

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return fmt.Errorf("创建本地目录失败: %w", err)
	}

	localFile, err := os.OpenFile(localPath, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("创建本地文件失败: %w", err)
	}
	defer localFile.Close()
	localInfo, err := localFile.Stat()
	if err != nil {
		return err
	}
	offset := localInfo.Size()
	if offset > stat.Size() {
		offset = 0
	}
	if offset == 0 {
		if err := localFile.Truncate(0); err != nil {
			return err
		}
	}
	if _, err := remoteFile.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("定位远程断点失败: %w", err)
	}
	if _, err := localFile.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("定位本地断点失败: %w", err)
	}

	buf := make([]byte, 64*1024)
	written := offset
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, err := remoteFile.Read(buf)
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 {
			break
		}
		if _, err := localFile.Write(buf[:n]); err != nil {
			return err
		}
		written += int64(n)
		c.report(progressChan, transferProgress("download", remotePath, stat.Size(), written, "running", attempt, offset > 0, false))
	}
	if err := localFile.Close(); err != nil {
		return err
	}
	c.report(progressChan, transferProgress("download", remotePath, stat.Size(), written, "verifying", attempt, offset > 0, false))
	remoteDigest, err := c.digestRemote(ctx, remotePath)
	if err != nil {
		return err
	}
	localDigest, err := digestLocal(ctx, localPath)
	if err != nil {
		return err
	}
	if localDigest != remoteDigest {
		return errChecksumMismatch
	}
	c.report(progressChan, transferProgress("download", remotePath, stat.Size(), written, "running", attempt, offset > 0, true))
	return nil
}

func (c *Client) digestRemote(ctx context.Context, path string) ([sha256.Size]byte, error) {
	f, err := c.sftp.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer f.Close()
	return digest(ctx, f)
}

func digestLocal(ctx context.Context, path string) ([sha256.Size]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer f.Close()
	return digest(ctx, f)
}

func digest(ctx context.Context, reader io.Reader) ([sha256.Size]byte, error) {
	hash := sha256.New()
	buffer := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return [sha256.Size]byte{}, err
		}
		n, err := reader.Read(buffer)
		if n > 0 {
			if _, writeErr := hash.Write(buffer[:n]); writeErr != nil {
				return [sha256.Size]byte{}, writeErr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return [sha256.Size]byte{}, err
		}
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func transferProgress(kind, path string, total, done int64, status string, attempt int, resumed, verified bool) TransferProgress {
	percent := float64(100)
	if total > 0 {
		percent = float64(done) / float64(total) * 100
	}
	fileName := filepath.Base(path)
	if kind == "download" {
		fileName = pathpkg.Base(path)
	}
	return TransferProgress{Type: kind, FileName: fileName, Total: total, Done: done, Percent: percent, Status: status, Attempt: attempt, Resumed: resumed, Verified: verified}
}

func (c *Client) report(progressChan chan<- TransferProgress, progress TransferProgress) {
	if progressChan != nil {
		progressChan <- progress
	}
}

func (c *Client) ReadFile(remotePath string) (string, error) {
	if err := c.connect(); err != nil {
		return "", err
	}
	f, err := c.sftp.Open(remotePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var buf []byte
	tmp := make([]byte, 32*1024)
	const maxPreviewSize = 2 << 20
	for {
		n, err := f.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if len(buf) > maxPreviewSize {
			return "", fmt.Errorf("文件超过 %d MB，拒绝预览", maxPreviewSize/(1<<20))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return string(buf), err
		}
	}
	return string(buf), nil
}

// ReadFileRange reads a bounded, one-based line range without loading the
// entire remote file. It reads one line beyond the requested range so callers
// can continue with NextStartLine when HasMore is true.
func (c *Client) ReadFileRange(remotePath string, startLine, lineCount int) (ReadRangeResult, error) {
	if startLine < 1 {
		return ReadRangeResult{}, fmt.Errorf("起始行必须大于等于 1")
	}
	if lineCount < 1 || lineCount > 1000 {
		return ReadRangeResult{}, fmt.Errorf("每次读取行数必须在 1 到 1000 之间")
	}
	if err := c.connect(); err != nil {
		return ReadRangeResult{}, err
	}
	f, err := c.sftp.Open(remotePath)
	if err != nil {
		return ReadRangeResult{}, err
	}
	defer f.Close()
	return readLineRange(bufio.NewReader(f), startLine, lineCount)
}

func readLineRange(reader *bufio.Reader, startLine, lineCount int) (ReadRangeResult, error) {
	result := ReadRangeResult{StartLine: startLine}
	var content strings.Builder
	lineNumber := 0
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			lineNumber++
			switch {
			case lineNumber >= startLine && result.ReturnedLines < lineCount:
				content.WriteString(line)
				result.EndLine = lineNumber
				result.ReturnedLines++
			case lineNumber >= startLine+lineCount:
				result.HasMore = true
				result.NextStartLine = lineNumber
				result.Content = content.String()
				return result, nil
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return ReadRangeResult{}, readErr
		}
	}
	result.Content = content.String()
	if result.EndLine > 0 && result.EndLine < lineNumber {
		result.HasMore = true
		result.NextStartLine = result.EndLine + 1
	}
	return result, nil
}

func (c *Client) WriteFile(remotePath, content string) error {
	if err := c.connect(); err != nil {
		return err
	}
	f, err := c.sftp.Create(remotePath)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write([]byte(content)); err != nil {
		return err
	}
	if _, ok := c.sftp.HasExtension(extensionFSync); ok {
		return f.Sync()
	}
	return nil
}

func (c *Client) GetCurrentDir() (string, error) {
	if err := c.connect(); err != nil {
		return "", err
	}
	return c.sftp.Getwd()
}

func (c *Client) syncRemoteFile(remotePath string) error {
	if _, ok := c.sftp.HasExtension(extensionFSync); !ok {
		return nil
	}
	f, err := c.sftp.OpenFile(remotePath, os.O_WRONLY)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func (c *Client) renameAtomic(oldPath, newPath string) error {
	if _, ok := c.sftp.HasExtension(extensionPosixRename); ok {
		return c.sftp.PosixRename(oldPath, newPath)
	}
	return c.sftp.Rename(oldPath, newPath)
}

func (c *Client) removeQuiet(remotePath string) error {
	if c.sftp == nil {
		return nil
	}
	if err := c.sftp.Remove(remotePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func uploadTempPath(remotePath string) string {
	dir := pathpkg.Dir(remotePath)
	base := pathpkg.Base(remotePath)
	return pathpkg.Join(dir, "."+base+".gossh-uploading")
}
