//go:build windows

package pipeline

// Windows 书级锁：LockFile 排他（分册 07 §2.4 的 msvcrt.locking 等价）。

import (
	"os"
	"syscall"
	"time"
)

const (
	lockRetryMax     = 10 // MSVCRT LK_LOCK：每 1s 重试至多 10 次后失败
	lockRetryDelay   = time.Second
)

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	procLockFile   = kernel32.NewProc("LockFile")
	procUnLockFile = kernel32.NewProc("UnlockFile")
)

func lockFile(f *os.File) error {
	// 空文件先写入 1 字节，保证锁定长度
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() == 0 {
		if _, err := f.WriteAt([]byte{0}, 0); err != nil {
			return err
		}
	}
	handle := f.Fd()
	for attempt := 0; attempt < lockRetryMax; attempt++ {
		r1, _, err := procLockFile.Call(handle, 0, 0, 1, 0)
		if r1 != 0 {
			return nil
		}
		if attempt < lockRetryMax-1 {
			time.Sleep(lockRetryDelay)
		} else {
			return err
		}
	}
	return os.ErrDeadlineExceeded
}

func unlockFile(f *os.File) error {
	r1, _, err := procUnLockFile.Call(f.Fd(), 0, 0, 1, 0)
	if r1 == 0 {
		return err
	}
	return nil
}
