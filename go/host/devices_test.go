package host

import (
	"context"
	"encoding/base64"
	"go.bug.st/serial"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// deviceFixture 创建独立设备管理器并注入假串口列表，避免测试探测或写入真实硬件。
func deviceFixture(t *testing.T) (*DeviceManager, context.Context) {
	t.Helper()
	m := NewDeviceManager(New(testConfig(), t.TempDir()))
	m.listSerial = func() ([]string, error) { return []string{"COM-TEST"}, nil }
	t.Cleanup(m.Shutdown)
	return m, desktopContext("device-test")
}

// waitDeviceState 等待后台接收状态变化，所有等待有上限，失败时输出真实快照。
func waitDeviceState(t *testing.T, m *DeviceManager, id, state string) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); {
		m.mu.Lock()
		entry := m.entries[id]
		actual := ""
		if entry != nil {
			actual = entry.State
		}
		m.mu.Unlock()
		if actual == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("device did not reach %s", state)
}

// TestSerialDeviceIsolationAndPackets 验证串口参数、独占、真实流式字节、窗口隔离和关闭清理。
func TestSerialDeviceIsolationAndPackets(t *testing.T) {
	m, ctx := deviceFixture(t)
	local, remote := net.Pipe()
	defer remote.Close()
	m.openSerial = func(name string, mode *serial.Mode) (devicePort, error) {
		if name != "COM-TEST" || mode.BaudRate != 9600 || mode.DataBits != 8 || mode.InitialStatusBits.DTR {
			t.Error("invalid serial mode")
		}
		return local, nil
	}
	if _, err := m.OpenSerial(context.Background(), SerialOptions{Port: "COM-TEST"}); err == nil {
		t.Fatal("permission bypass")
	}
	if _, err := m.OpenSerial(ctx, SerialOptions{Port: "C:/private.txt"}); err == nil {
		t.Fatal("arbitrary file accepted")
	}
	value, err := m.OpenSerial(ctx, SerialOptions{Port: "COM-TEST"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.OpenSerial(ctx, SerialOptions{Port: "COM-TEST"}); err == nil {
		t.Fatal("serial sharing accepted")
	}
	go func() { _, _ = remote.Write([]byte("ST,GS,+12.340kg\r\n条码\n")) }()
	result, err := m.Read(ctx, value.ID, 1000)
	if err != nil || len(result.Packets) != 1 || !strings.Contains(string(result.Packets[0].Data), "条码") {
		t.Fatal(err, result)
	}
	encoded, _ := base64.StdEncoding.DecodeString(base64.StdEncoding.EncodeToString(result.Packets[0].Data))
	if string(encoded) != string(result.Packets[0].Data) {
		t.Fatal("byte conversion")
	}
	if _, err = m.Read(desktopContext("another-window"), value.ID, 1); err == nil {
		t.Fatal("cross-window read")
	}
	if _, err = m.Read(bridgeContext("https://app.test", "device-test", "host"), value.ID, 1); err == nil {
		t.Fatal("same-window capability bypass")
	}
	if _, err = m.Write(bridgeContext("https://other.test", "device-test", "devices.serial"), value.ID, []byte("x")); err == nil {
		t.Fatal("cross-origin write")
	}
	m.CloseWindow("device-test")
	if _, err = m.Read(ctx, value.ID, 1); err == nil {
		t.Fatal("closed window retained connection")
	}
}

// TestDeviceReadHandoff 验证停止等待会真正释放读取权，重新监听不需要关闭串口或丢弃数据。
func TestDeviceReadHandoff(t *testing.T) {
	m, ctx := deviceFixture(t)
	local, remote := net.Pipe()
	defer remote.Close()
	m.openSerial = func(string, *serial.Mode) (devicePort, error) { return local, nil }
	value, err := m.OpenSerial(ctx, SerialOptions{Port: "COM-TEST"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := m.Read(ctx, value.ID, 5000); done <- err }()
	for end := time.Now().Add(time.Second); time.Now().Before(end); {
		m.mu.Lock()
		reading := m.entries[value.ID].reading
		m.mu.Unlock()
		if reading {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err = m.CancelRead(ctx, value.ID); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err == nil {
		t.Fatal("read not cancelled")
	}
	if _, err = m.Read(ctx, value.ID, 1); err != nil {
		t.Fatal("old read retained ownership", err)
	}
}

// TestDeviceWriteGuardAndCancellation 验证实际写入期间阻止重启，取消会关闭连接，随后释放退出保护。
func TestDeviceWriteGuardAndCancellation(t *testing.T) {
	m, ctx := deviceFixture(t)
	local, remote := net.Pipe()
	defer remote.Close()
	m.openSerial = func(string, *serial.Mode) (devicePort, error) { return local, nil }
	value, err := m.OpenSerial(ctx, SerialOptions{Port: "COM-TEST"})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan error, 1)
	go func() { _, err := m.Write(ctx, value.ID, []byte("COMMAND")); started <- err }()
	for end := time.Now().Add(time.Second); time.Now().Before(end); {
		if len(m.controller.Tasks()) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if undo, err := m.controller.reserveMaintenance(); err == nil {
		undo()
		t.Fatal("restart allowed during write")
	}
	buffer := make([]byte, 7)
	if _, err = io.ReadFull(remote, buffer); err != nil || string(buffer) != "COMMAND" {
		t.Fatal(err, string(buffer))
	}
	if err = <-started; err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	go func() { _, err := m.Write(cancelCtx, value.ID, []byte("BLOCKED")); started <- err }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err = <-started; err == nil {
		t.Fatal("cancel ignored")
	}
	waitDeviceState(t, m, value.ID, "closed")
	for end := time.Now().Add(time.Second); time.Now().Before(end) && len(m.controller.Tasks()) > 0; {
		time.Sleep(time.Millisecond)
	}
	if len(m.controller.Tasks()) != 0 {
		t.Fatal("write guard leaked")
	}
}

// TestDeviceTCPWhitelist 使用本机临时 TCP 回显服务验证真实网络通信，不访问外网或打印机。
func TestDeviceTCPWhitelist(t *testing.T) {
	m, ctx := deviceFixture(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	endpoint := listener.Addr().String()
	if _, err = m.OpenTCP(ctx, endpoint); err == nil {
		t.Fatal("whitelist bypass")
	}
	m.controller.config.DeviceEndpoints = []string{endpoint}
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = io.Copy(connection, connection)
	}()
	value, err := m.OpenTCP(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := m.Write(ctx, value.ID, []byte("ABC")); err != nil || n != 3 {
		t.Fatal(err, n)
	}
	result, err := m.Read(ctx, value.ID, 1000)
	if err != nil || len(result.Packets) != 1 || string(result.Packets[0].Data) != "ABC" {
		t.Fatal(err, result)
	}
	if err = m.Close(ctx, value.ID, true); err != nil {
		t.Fatal(err)
	}
}

// TestDeviceBackpressureAndTerminalDrain 验证积压明确报错，连接断开时多于一页的已接收数据仍可完整取出。
func TestDeviceBackpressureAndTerminalDrain(t *testing.T) {
	m, ctx := deviceFixture(t)
	local, remote := net.Pipe()
	defer remote.Close()
	m.openSerial = func(string, *serial.Mode) (devicePort, error) { return local, nil }
	value, err := m.OpenSerial(ctx, SerialOptions{Port: "COM-TEST"})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range 70 {
			if _, err := remote.Write(make([]byte, 4096)); err != nil {
				return
			}
		}
	}()
	waitDeviceState(t, m, value.ID, "failed")
	m.mu.Lock()
	code := m.entries[value.ID].Error.Code
	m.mu.Unlock()
	if code != "E_BACKPRESSURE" {
		t.Fatal(code)
	}
	total := 0
	for range 8 {
		page, err := m.Read(ctx, value.ID, 1)
		if err != nil {
			t.Fatal(err)
		}
		total += len(page.Packets)
		if page.State != "connected" {
			break
		}
	}
	if total != 64 {
		t.Fatalf("terminal packets lost: %d", total)
	}
}

// TestDeviceModeAndIdleLease 验证非法串口参数与空权限打印请求被拒绝，闲置连接被回收。
func TestDeviceModeAndIdleLease(t *testing.T) {
	for _, options := range []SerialOptions{{BaudRate: -1}, {DataBits: 9}, {Parity: "bad"}, {StopBits: "3"}} {
		if _, err := serialMode(options); err == nil {
			t.Fatal("invalid mode", options)
		}
	}
	m, ctx := deviceFixture(t)
	local, remote := net.Pipe()
	defer remote.Close()
	m.openSerial = func(string, *serial.Mode) (devicePort, error) { return local, nil }
	value, err := m.OpenSerial(ctx, SerialOptions{Port: "COM-TEST"})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.entries[value.ID].seen = time.Now().Add(-2 * time.Minute)
	m.mu.Unlock()
	m.expire()
	waitDeviceState(t, m, value.ID, "disconnected")
	if _, err = m.PrintRaw(context.Background(), "none", "test", []byte("test")); err == nil {
		t.Fatal("RAW permission bypass")
	}
}
