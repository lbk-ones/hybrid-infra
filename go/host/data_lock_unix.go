//go:build !windows

package host

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"sync"
)

// acquireDataLock 通过操作系统文件锁保护整段应用生命周期，进程异常退出也会释放。
func acquireDataLock(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, ".data.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, Fail("E_DATA_IN_USE", "此数据目录已由其他进程使用，请使用独立 HYBRID_DATA_DIR")
	}
	var once sync.Once
	return func() { once.Do(func() { _ = file.Close() }) }, nil
}
