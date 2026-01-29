//go:build windows || darwin || linux

package cross

type BOX struct {
	Left, Top, Width, Height int32
}
