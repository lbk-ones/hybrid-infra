//go:build windows

package host

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var kernel32 = windows.NewLazySystemDLL("kernel32.dll")
var advapi32 = windows.NewLazySystemDLL("advapi32.dll")

// revealPath 使用资源管理器选择指定文件；目录直接打开，参数逐项传递，不经过命令解释器。
func revealPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return diskError(err)
	}
	args := []string{path}
	if !info.IsDir() {
		args = []string{"/select,", path}
	}
	cmd := exec.Command("explorer.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err = cmd.Start(); err != nil {
		return Fail("E_SYSTEM", "无法打开资源管理器")
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// clipboardImage 使用 Windows 注册 PNG 格式读写图像；读取同时兼容常见截图工具提供的 24/32 位 CF_DIB。
// 剪贴板句柄只能在 OpenClipboard 到 CloseClipboard 之间使用，写入成功后内存所有权转移给系统。
func clipboardImage(data []byte, write bool, owner uintptr) ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if !win.OpenClipboard(win.HWND(owner)) {
		return nil, Fail("E_BUSY", "剪贴板正被其他程序占用，请稍后重试")
	}
	defer win.CloseClipboard()
	name, _ := windows.UTF16PtrFromString("PNG")
	format, _, _ := user32.NewProc("RegisterClipboardFormatW").Call(uintptr(unsafe.Pointer(name)))
	if format == 0 {
		return nil, Fail("E_SYSTEM", "无法注册剪贴板图像格式")
	}
	if write {
		dib, err := pngDIB(data)
		if err != nil {
			return nil, err
		}
		// 提前分配内存，只有准备完成后才清空原剪贴板。
		h := win.GlobalAlloc(win.GMEM_MOVEABLE, uintptr(len(data)))
		if h == 0 {
			return nil, Fail("E_SYSTEM", "无法分配剪贴板内存")
		}
		p := win.GlobalLock(h)
		if p == nil {
			win.GlobalFree(h)
			return nil, Fail("E_SYSTEM", "无法锁定剪贴板内存")
		}
		copy(unsafe.Slice((*byte)(p), len(data)), data)
		win.GlobalUnlock(h)
		if !win.EmptyClipboard() || win.SetClipboardData(uint32(format), win.HANDLE(h)) == 0 {
			win.GlobalFree(h)
			return nil, Fail("E_SYSTEM", "写入剪贴板失败")
		}
		bitmap := win.GlobalAlloc(win.GMEM_MOVEABLE, uintptr(len(dib)))
		if bitmap == 0 {
			return nil, Fail("E_SYSTEM", "PNG 已复制，但无法分配兼容位图内存")
		}
		pixels := win.GlobalLock(bitmap)
		if pixels == nil {
			win.GlobalFree(bitmap)
			return nil, Fail("E_SYSTEM", "PNG 已复制，但兼容位图写入失败")
		}
		copy(unsafe.Slice((*byte)(pixels), len(dib)), dib)
		win.GlobalUnlock(bitmap)
		if win.SetClipboardData(win.CF_DIB, win.HANDLE(bitmap)) == 0 {
			win.GlobalFree(bitmap)
			return nil, Fail("E_SYSTEM", "PNG 已复制，但兼容位图写入失败")
		}
		return nil, nil
	}
	h := win.GetClipboardData(uint32(format))
	dib := false
	if h == 0 {
		h = win.GetClipboardData(win.CF_DIB)
		dib = true
	}
	if h == 0 {
		return nil, Fail("E_NOT_FOUND", "剪贴板中没有支持的图像")
	}
	size, _, _ := kernel32.NewProc("GlobalSize").Call(uintptr(h))
	if size == 0 || size > maxFileBytes {
		return nil, Fail("E_TOO_LARGE", "剪贴板图像为空或超过 512 KiB")
	}
	p := win.GlobalLock(win.HGLOBAL(h))
	if p == nil {
		return nil, Fail("E_SYSTEM", "无法读取剪贴板图像")
	}
	defer win.GlobalUnlock(win.HGLOBAL(h))
	result := append([]byte(nil), unsafe.Slice((*byte)(p), int(size))...)
	if dib {
		return dibToPNG(result)
	}
	return result, nil
}

// nativeCredential 按 target 读写或删除 Windows 通用凭据，秘密仅由当前用户的系统凭据管理器保存，不落普通 JSON。
func nativeCredential(target string, value []byte, operation string) ([]byte, error) {
	targetW, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return nil, Fail("E_INVALID_ARGUMENT", "凭据名称无效")
	}
	if operation == "set" {
		c := nativeCredentialRecord{Type: 1, TargetName: targetW, CredentialBlobSize: uint32(len(value)), Persist: 2}
		if len(value) > 0 {
			c.CredentialBlob = &value[0]
		}
		ok, _, _ := advapi32.NewProc("CredWriteW").Call(uintptr(unsafe.Pointer(&c)), 0)
		runtime.KeepAlive(value)
		if ok == 0 {
			return nil, Fail("E_CREDENTIAL", "系统凭据写入失败")
		}
		return nil, nil
	}
	if operation == "delete" {
		ok, _, err := advapi32.NewProc("CredDeleteW").Call(uintptr(unsafe.Pointer(targetW)), 1, 0)
		if ok == 0 && err != windows.ERROR_NOT_FOUND {
			return nil, Fail("E_CREDENTIAL", "系统凭据删除失败")
		}
		return nil, nil
	}
	var record *nativeCredentialRecord
	ok, _, err := advapi32.NewProc("CredReadW").Call(uintptr(unsafe.Pointer(targetW)), 1, 0, uintptr(unsafe.Pointer(&record)))
	if ok == 0 {
		if err == windows.ERROR_NOT_FOUND {
			return nil, Fail("E_NOT_FOUND", "凭据不存在")
		}
		return nil, Fail("E_CREDENTIAL", "系统凭据读取失败")
	}
	defer advapi32.NewProc("CredFree").Call(uintptr(unsafe.Pointer(record)))
	if record.CredentialBlobSize > 2560 {
		return nil, Fail("E_TOO_LARGE", "凭据长度超过限制")
	}
	return append([]byte(nil), unsafe.Slice(record.CredentialBlob, int(record.CredentialBlobSize))...), nil
}

// nativeCredentialRecord 对应 Win32 CREDENTIALW，使用系统布局保存指针与 FILETIME，不对前端暴露。
type nativeCredentialRecord struct {
	Flags, Type             uint32
	TargetName, Comment     *uint16
	LastWritten             windows.Filetime
	CredentialBlobSize      uint32
	CredentialBlob          *byte
	Persist, AttributeCount uint32
	Attributes              uintptr
	TargetAlias, UserName   *uint16
}

// registerProtocol 为当前应用身份派生的协议注册用户级入口；核对命令归属，避免覆盖或删除其他安装的注册。
func registerProtocol(appID, scheme string, enable bool) error {
	if !validID.MatchString(scheme) || strings.ToLower(scheme) != scheme {
		return Fail("E_INVALID_ARGUMENT", "协议名称无效")
	}
	exe, err := os.Executable()
	if err != nil {
		return Fail("E_SYSTEM", "无法确定程序路径")
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return Fail("E_SYSTEM", "程序路径无效")
	}
	path := `Software\Classes\` + scheme
	command := `"` + exe + `" "%1"`
	if appID != "hybrid-infra" {
		command = `"` + exe + `" --hybrid-app-id=` + appID + ` "%1"`
	}
	if !enable {
		key, e := registry.OpenKey(registry.CURRENT_USER, path+`\shell\open\command`, registry.QUERY_VALUE)
		if e == registry.ErrNotExist {
			return nil
		}
		if e != nil {
			return Fail("E_SYSTEM", "无法读取协议注册")
		}
		current, _, e := key.GetStringValue("")
		key.Close()
		if e != nil || current != command {
			return Fail("E_PERMISSION_DENIED", "协议当前属于其他安装，拒绝移除")
		}
		for _, suffix := range []string{`\shell\open\command`, `\shell\open`, `\shell`, ""} {
			if e = registry.DeleteKey(registry.CURRENT_USER, path+suffix); e != nil && e != registry.ErrNotExist {
				return Fail("E_SYSTEM", "移除协议注册失败")
			}
		}
		return nil
	}
	existing, e := registry.OpenKey(registry.CURRENT_USER, path+`\shell\open\command`, registry.QUERY_VALUE)
	if e == nil {
		current, _, readErr := existing.GetStringValue("")
		existing.Close()
		if readErr != nil || current != command {
			return Fail("E_CONFLICT", "协议已被其他安装注册")
		}
	} else if e != registry.ErrNotExist {
		return Fail("E_SYSTEM", "无法读取协议注册")
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return Fail("E_SYSTEM", "无法注册协议")
	}
	defer key.Close()
	if err = key.SetStringValue("", "URL:Hybrid Infra"); err != nil {
		return Fail("E_SYSTEM", "无法写入协议说明")
	}
	if err = key.SetStringValue("URL Protocol", ""); err != nil {
		return Fail("E_SYSTEM", "无法写入协议标记")
	}
	cmd, _, err := registry.CreateKey(registry.CURRENT_USER, path+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return Fail("E_SYSTEM", "无法注册协议命令")
	}
	defer cmd.Close()
	if err = cmd.SetStringValue("", command); err != nil {
		return Fail("E_SYSTEM", "无法保存协议命令")
	}
	return nil
}

// dibToPNG 把标准无压缩 24/32 位 Windows DIB 转为 PNG；拒绝调色板、压缩或超大图像，避免不可信剪贴板数据导致越界。
func dibToPNG(data []byte) ([]byte, error) {
	if len(data) < 40 {
		return nil, Fail("E_UNSUPPORTED", "剪贴板位图头不完整")
	}
	header := int(binary.LittleEndian.Uint32(data))
	width := int(int32(binary.LittleEndian.Uint32(data[4:])))
	height := int(int32(binary.LittleEndian.Uint32(data[8:])))
	bits := int(binary.LittleEndian.Uint16(data[14:]))
	compression := binary.LittleEndian.Uint32(data[16:])
	if header < 40 || header > len(data) || width < 1 || width > 8192 || height == 0 || height > 8192 || height < -8192 || (bits != 24 && bits != 32) || compression != 0 {
		return nil, Fail("E_UNSUPPORTED", "仅支持无压缩的 24/32 位剪贴板位图")
	}
	return encodeDIB(data[header:], width, height, bits)
}
