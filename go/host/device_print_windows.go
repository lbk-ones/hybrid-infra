//go:build windows

package host

import (
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
	"sort"
	"unsafe"
)

var deviceSpool = windows.NewLazySystemDLL("winspool.drv")

// devicePrinters 使用本机缓存的四级枚举，不逐一连接远程打印机；返回实际安装名称。
func devicePrinters() ([]string, error) {
	var needed, count uint32
	_ = win.EnumPrinters(6, nil, 4, nil, 0, &needed, &count)
	if needed == 0 {
		return []string{}, nil
	}
	if needed > 4<<20 {
		return nil, Fail("E_DEVICE_ENUM", "打印机列表过大")
	}
	buffer := make([]byte, needed)
	if !win.EnumPrinters(6, nil, 4, &buffer[0], needed, &needed, &count) || uint64(count)*uint64(unsafe.Sizeof(win.PRINTER_INFO_4{})) > uint64(len(buffer)) {
		return nil, Fail("E_DEVICE_ENUM", "无法枚举打印机")
	}
	values := []string{}
	for _, entry := range unsafe.Slice((*win.PRINTER_INFO_4)(unsafe.Pointer(&buffer[0])), int(count)) {
		if entry.PPrinterName != nil {
			values = append(values, windows.UTF16PtrToString(entry.PPrinterName))
		}
	}
	sort.Strings(values)
	return values, nil
}

// devicePrintRaw 以 RAW 数据类型提交指令，失败时终止未完成后台作业，不做格式转换或自动重试。
func devicePrintRaw(printer, title string, data []byte) (uint32, error) {
	name, err := windows.UTF16PtrFromString(printer)
	if err != nil {
		return 0, Fail("E_INVALID_ARGUMENT", "打印机名称无效")
	}
	label, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return 0, Fail("E_INVALID_ARGUMENT", "打印标题无效")
	}
	raw, _ := windows.UTF16PtrFromString("RAW")
	var handle windows.Handle
	ok, _, _ := deviceSpool.NewProc("OpenPrinterW").Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&handle)), 0)
	if ok == 0 {
		return 0, Fail("E_DEVICE_PRINT", "无法打开打印机")
	}
	defer deviceSpool.NewProc("ClosePrinter").Call(uintptr(handle))
	doc := struct{ Name, Output, Type *uint16 }{label, nil, raw}
	id, _, _ := deviceSpool.NewProc("StartDocPrinterW").Call(uintptr(handle), 1, uintptr(unsafe.Pointer(&doc)))
	if id == 0 {
		return 0, Fail("E_DEVICE_PRINT", "无法建立 RAW 打印作业")
	}
	complete := false
	defer func() {
		if !complete {
			deviceSpool.NewProc("AbortPrinter").Call(uintptr(handle))
		}
	}()
	ok, _, _ = deviceSpool.NewProc("StartPagePrinter").Call(uintptr(handle))
	if ok == 0 {
		return 0, Fail("E_DEVICE_PRINT", "无法开始打印页")
	}
	for offset := 0; offset < len(data); {
		var written uint32
		size := min(len(data)-offset, 16384)
		ok, _, _ = deviceSpool.NewProc("WritePrinter").Call(uintptr(handle), uintptr(unsafe.Pointer(&data[offset])), uintptr(size), uintptr(unsafe.Pointer(&written)))
		if ok == 0 || written == 0 || written > uint32(size) {
			return 0, Fail("E_DEVICE_PRINT", "RAW 发送失败，可能已提交部分打印数据")
		}
		offset += int(written)
	}
	ok, _, _ = deviceSpool.NewProc("EndPagePrinter").Call(uintptr(handle))
	if ok == 0 {
		return 0, Fail("E_DEVICE_PRINT", "打印页提交失败")
	}
	ok, _, _ = deviceSpool.NewProc("EndDocPrinter").Call(uintptr(handle))
	if ok == 0 {
		return 0, Fail("E_DEVICE_PRINT", "打印后台提交失败")
	}
	complete = true
	return uint32(id), nil
}
