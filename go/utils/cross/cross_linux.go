//go:build linux

package cross

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Linux X11相关常量和结构体（X11窗口系统，主流Linux发行版默认）
const (
	netWorkArea = "_NET_WORKAREA"
	utf8String  = "UTF8_STRING"
)

type xDisplay struct {
	_ [0]func() // 空结构体，占位X11 Display指针
}

type xAtom struct {
	_ int32 // 占位X11 Atom类型
}

var (
	// 引用X11库（Linux窗口管理核心）
	libX11                 = syscall.NewLazyDLL("libX11.so.6")
	procXOpenDisplay       = libX11.NewProc("XOpenDisplay")
	procXInternAtom        = libX11.NewProc("XInternAtom")
	procXGetWindowProperty = libX11.NewProc("XGetWindowProperty")
	procXCloseDisplay      = libX11.NewProc("XCloseDisplay")
)

// GetScreenSize Linux(X11)平台获取指定显示器的工作区
func GetScreenSize() (BOX, error) {
	index := 0
	// 打开X11显示连接
	display, _, err := procXOpenDisplay.Call(0)
	if display == 0 {
		return BOX{}, fmt.Errorf("Linux XOpenDisplay失败：%v", err)
	}
	defer procXCloseDisplay.Call(display)

	// 获取_NET_WORKAREA原子（X11标准，用于获取工作区）
	netWorkAreaAtom, _, err := procXInternAtom.Call(
		display,
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(netWorkArea))),
		uintptr(0),
	)
	if netWorkAreaAtom == 0 {
		return BOX{}, fmt.Errorf("Linux XInternAtom(_NET_WORKAREA)失败：%v", err)
	}

	// 获取根窗口属性（_NET_WORKAREA存储工作区信息）
	var (
		actualType   uintptr
		actualFormat int32
		nItems       uintptr
		bytesAfter   uintptr
		propRet      uintptr
	)
	ret, _, err := procXGetWindowProperty.Call(
		display,
		uintptr(0x00000000), // X11根窗口句柄（DefaultRootWindow）
		netWorkAreaAtom,
		uintptr(0),
		uintptr(100), // 读取数据长度
		uintptr(0),
		uintptr(utf8String),
		uintptr(unsafe.Pointer(&actualType)),
		uintptr(unsafe.Pointer(&actualFormat)),
		uintptr(unsafe.Pointer(&nItems)),
		uintptr(unsafe.Pointer(&bytesAfter)),
		uintptr(unsafe.Pointer(&propRet)),
	)
	if ret != 0 || propRet == 0 {
		return BOX{}, fmt.Errorf("Linux XGetWindowProperty失败：%v", err)
	}
	defer syscall.Free(syscall.Pointer(propRet))

	// 解析_NET_WORKAREA数据：[]int32{left, top, width, height, ...}（多显示器按索引排列）
	data := (*[1 << 20]int32)(unsafe.Pointer(propRet))[:nItems:nItems]
	if index*4+3 >= len(data) {
		return BOX{}, errors.New("Linux获取指定显示器工作区索引越界")
	}
	// 按索引取对应显示器的工作区数据
	offset := index * 4
	left := int(data[offset])
	top := int(data[offset+1])
	width := int(data[offset+2])
	height := int(data[offset+3])

	return BOX{
		Left:   left,
		Top:    top,
		Width:  width,
		Height: height,
	}, nil
}
