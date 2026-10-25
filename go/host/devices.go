package host

import (
	"context"
	"errors"
	"go.bug.st/serial"
	"io"
	"net"
	"slices"
	"sort"
	"sync"
	"time"
)

// SerialOptions 明确串口通信参数；DTR/RTS 默认关闭，打开真实设备仍可能受驱动影响产生短脉冲。
type SerialOptions struct {
	Port     string `json:"port"`
	BaudRate int    `json:"baudRate"`
	DataBits int    `json:"dataBits"`
	Parity   string `json:"parity"`
	StopBits string `json:"stopBits"`
	DTR      bool   `json:"dtr"`
	RTS      bool   `json:"rts"`
}

// DeviceConnection 是当前来源与窗口的连接快照，id 是临时凭证，不是操作系统句柄。
type DeviceConnection struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	State string `json:"state"`
	Error *Error `json:"error,omitempty"`
}

// DevicePacket 保留读取顺序；data 通过绑定编码为 Base64，SDK 转回字节，不猜测设备的协议和字符集。
type DevicePacket struct {
	Sequence int64  `json:"sequence"`
	Data     []byte `json:"data"`
}

// DeviceRead 一次返回最多 16 个数据块及连接状态；终态中仍会先交付已入队数据，不静默丢包。
type DeviceRead struct {
	Packets []DevicePacket `json:"packets"`
	State   string         `json:"state"`
	Error   *Error         `json:"error,omitempty"`
}
type devicePort interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}
type deviceEntry struct {
	DeviceConnection
	owner, window    string
	port             devicePort
	queue            []DevicePacket
	bytes            int
	sequence         int64
	wake             chan struct{}
	seen             time.Time
	reading, writing bool
	readEpoch        uint64
}

// DeviceManager 管理最多八个设备连接与有界接收队列，窗口关闭、会话失联或应用退出都会关闭所属连接。
type DeviceManager struct {
	mu         sync.Mutex
	controller *Controller
	entries    map[string]*deviceEntry
	closed     bool
	stop       chan struct{}
	listSerial func() ([]string, error)
	openSerial func(string, *serial.Mode) (devicePort, error)
	printJobs  chan struct{}
}

// NewDeviceManager 创建通信管理器，读取固定启动白名单；测试可替换串口驱动，不访问真实设备。
func NewDeviceManager(c *Controller) *DeviceManager {
	m := &DeviceManager{controller: c, entries: map[string]*deviceEntry{}, stop: make(chan struct{}), listSerial: serial.GetPortsList, openSerial: func(name string, mode *serial.Mode) (devicePort, error) {
		port, err := serial.Open(name, mode)
		if err != nil {
			return nil, err
		}
		if err = port.SetReadTimeout(250 * time.Millisecond); err != nil {
			_ = port.Close()
			return nil, err
		}
		return port, nil
	}, printJobs: make(chan struct{}, 2)}
	// 无回调或页面异常离开时，最多闲置一分钟再撤销连接，避免刷新后串口被旧页面一直占用。
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.expire()
			case <-m.stop:
				return
			}
		}
	}()
	return m
}

// serialMode 校验参数并映射为驱动设置，不将设备名解释为任意文件路径。
func serialMode(options SerialOptions) (*serial.Mode, error) {
	if options.BaudRate == 0 {
		options.BaudRate = 9600
	}
	if options.DataBits == 0 {
		options.DataBits = 8
	}
	if options.BaudRate < 50 || options.BaudRate > 4000000 || options.DataBits < 5 || options.DataBits > 8 {
		return nil, Fail("E_INVALID_ARGUMENT", "波特率应为 50–4000000，数据位为 5–8")
	}
	parity := serial.NoParity
	switch options.Parity {
	case "", "none":
	case "odd":
		parity = serial.OddParity
	case "even":
		parity = serial.EvenParity
	case "mark":
		parity = serial.MarkParity
	case "space":
		parity = serial.SpaceParity
	default:
		return nil, Fail("E_INVALID_ARGUMENT", "校验位仅支持 none/odd/even/mark/space")
	}
	stop := serial.OneStopBit
	switch options.StopBits {
	case "", "1":
	case "1.5":
		stop = serial.OnePointFiveStopBits
	case "2":
		stop = serial.TwoStopBits
	default:
		return nil, Fail("E_INVALID_ARGUMENT", "停止位应为 1、1.5 或 2")
	}
	return &serial.Mode{BaudRate: options.BaudRate, DataBits: options.DataBits, Parity: parity, StopBits: stop, InitialStatusBits: &serial.ModemOutputBits{DTR: options.DTR, RTS: options.RTS}}, nil
}

// ListPorts 只枚举操作系统报告的串口名称，不自动打开或发送探测命令。
func (m *DeviceManager) ListPorts(ctx context.Context) ([]string, error) {
	if err := Require(ctx, "devices.serial"); err != nil {
		return nil, err
	}
	ports, err := m.listSerial()
	if err != nil {
		return nil, Fail("E_DEVICE_ENUM", "无法枚举串口")
	}
	if ports == nil {
		ports = []string{}
	}
	sort.Strings(ports)
	return ports, nil
}

// OpenSerial 只允许打开当前枚举到的串口，不允许借串口 API 指定任意磁盘文件。
func (m *DeviceManager) OpenSerial(ctx context.Context, options SerialOptions) (DeviceConnection, error) {
	ports, err := m.ListPorts(ctx)
	if err != nil {
		return DeviceConnection{}, err
	}
	if !slices.Contains(ports, options.Port) {
		return DeviceConnection{}, Fail("E_DEVICE_NOT_FOUND", "串口不存在，请重新枚举")
	}
	mode, err := serialMode(options)
	if err != nil {
		return DeviceConnection{}, err
	}
	return m.open(ctx, "serial", options.Port, func() (devicePort, error) { return m.openSerial(options.Port, mode) })
}

// OpenTCP 只连接启动配置 deviceEndpoints 中的字面 IP:端口，不解析域名，不自动扫描网络或发送指令。
func (m *DeviceManager) OpenTCP(ctx context.Context, endpoint string) (DeviceConnection, error) {
	if err := Require(ctx, "devices.network"); err != nil {
		return DeviceConnection{}, err
	}
	if !slices.Contains(m.controller.config.DeviceEndpoints, endpoint) {
		return DeviceConnection{}, Fail("E_DEVICE_NOT_ALLOWED", "网络地址未列入 deviceEndpoints 白名单")
	}
	return m.open(ctx, "tcp", endpoint, func() (devicePort, error) {
		dialer := net.Dialer{Timeout: 5 * time.Second}
		return dialer.DialContext(ctx, "tcp", endpoint)
	})
}

// open 先预留连接和退出保护，再在锁外打开驱动；窗口关闭或调用取消时不把迟到连接交给旧页面。
func (m *DeviceManager) open(ctx context.Context, kind, name string, connect func() (devicePort, error)) (DeviceConnection, error) {
	if ctx.Err() != nil {
		return DeviceConnection{}, Fail("E_CANCELLED", "设备连接请求已取消")
	}
	if caller(ctx) == "" {
		return DeviceConnection{}, Fail("E_WINDOW_NOT_FOUND", "设备连接需要原生窗口")
	}
	guard, err := m.controller.BeginTask(ctx, "连接设备")
	if err != nil {
		return DeviceConnection{}, err
	}
	defer m.controller.EndTask(ctx, guard.ID)
	id, err := randomID()
	if err != nil {
		return DeviceConnection{}, err
	}
	entry := &deviceEntry{DeviceConnection: DeviceConnection{ID: id, Kind: kind, Name: name, State: "opening"}, owner: resourceOwner(ctx), window: caller(ctx), wake: make(chan struct{}, 1), seen: time.Now()}
	m.mu.Lock()
	active, owned := 0, 0
	for _, current := range m.entries {
		if current.State == "connected" || current.State == "opening" {
			active++
			if current.owner == entry.owner {
				owned++
			}
			if kind == "serial" && current.Kind == kind && current.Name == name {
				m.mu.Unlock()
				return DeviceConnection{}, Fail("E_DEVICE_BUSY", "串口已被另一个连接占用")
			}
		}
	}
	if m.closed || active >= 8 || owned >= 4 || len(m.entries) >= 64 {
		m.mu.Unlock()
		return DeviceConnection{}, Fail("E_DEVICE_BUSY", "设备连接已达上限或正在退出，请关闭并移除旧记录")
	}
	m.entries[id] = entry
	m.mu.Unlock()
	port, err := connect()
	m.mu.Lock()
	if err != nil || ctx.Err() != nil || m.closed || entry.State != "opening" {
		entry.State = "failed"
		entry.Error = &Error{Code: "E_DEVICE_OPEN", Message: "设备打开失败、被占用、取消或窗口已关闭"}
		m.mu.Unlock()
		if port != nil {
			_ = port.Close()
		}
		return DeviceConnection{}, entry.Error
	}
	entry.port = port
	entry.State = "connected"
	info := entry.DeviceConnection
	m.mu.Unlock()
	go m.receive(entry)
	return info, nil
}

// deviceCapability 根据实际连接类型检查权限，Read/Write 不接受由网页指定的能力名称。
func deviceCapability(kind string) string {
	if kind == "serial" {
		return "devices.serial"
	}
	return "devices.network"
}

// ownedLocked 在持锁时校验临时凭证和窗口归属，不允许其他来源复用连接 ID。
func (m *DeviceManager) ownedLocked(ctx context.Context, id string) (*deviceEntry, error) {
	entry := m.entries[id]
	if entry == nil || entry.owner != resourceOwner(ctx) {
		return nil, Fail("E_PERMISSION_DENIED", "设备连接不存在或不属于当前窗口")
	}
	if err := Require(ctx, deviceCapability(entry.Kind)); err != nil {
		return nil, err
	}
	return entry, nil
}

// wakeDevice 唤醒一个接收等待者，不阻塞驱动读取，也不在锁内调用网页回调。
func wakeDevice(entry *deviceEntry) {
	select {
	case entry.wake <- struct{}{}:
	default:
	}
}

// receive 连续读取原始数据，队列最多 64 块、256 KiB；积压超限关闭连接并明确报错，避免无提示丢包。
func (m *DeviceManager) receive(entry *deviceEntry) {
	buffer := make([]byte, 4096)
	for {
		n, err := entry.port.Read(buffer)
		m.mu.Lock()
		if m.closed || entry.State != "connected" {
			m.mu.Unlock()
			return
		}
		if n > 0 {
			if len(entry.queue) >= 64 || entry.bytes+n > 256<<10 {
				m.mu.Unlock()
				m.stopEntry(entry, "failed", &Error{Code: "E_BACKPRESSURE", Message: "接收队列积压，请提高处理速度后重新连接"})
				return
			}
			entry.sequence++
			entry.queue = append(entry.queue, DevicePacket{entry.sequence, append([]byte(nil), buffer[:n]...)})
			entry.bytes += n
			wakeDevice(entry)
		}
		m.mu.Unlock()
		if err != nil {
			message := "设备已断开"
			if errors.Is(err, io.EOF) {
				message = "设备已结束连接"
			}
			m.stopEntry(entry, "disconnected", &Error{Code: "E_DEVICE_DISCONNECTED", Message: message})
			return
		}
	}
}

// Read 等待真实数据或终态，每个连接只允许一个接收者；取消等待不会自动撤销设备已产生的数据。
func (m *DeviceManager) Read(ctx context.Context, id string, timeoutMs int) (DeviceRead, error) {
	result := DeviceRead{Packets: []DevicePacket{}}
	if timeoutMs == 0 {
		timeoutMs = 5000
	}
	if timeoutMs < 1 || timeoutMs > 10000 {
		return result, Fail("E_INVALID_ARGUMENT", "读取等待应为 1–10000 毫秒")
	}
	m.mu.Lock()
	entry, err := m.ownedLocked(ctx, id)
	if err != nil {
		m.mu.Unlock()
		return result, err
	}
	if entry.reading {
		m.mu.Unlock()
		return result, Fail("E_DEVICE_BUSY", "当前连接已有接收者，请共用 SDK onData")
	}
	entry.reading = true
	epoch := entry.readEpoch
	entry.seen = time.Now()
	m.mu.Unlock()
	defer func() { m.mu.Lock(); entry.reading = false; m.mu.Unlock() }()
	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()
	for {
		m.mu.Lock()
		if epoch != entry.readEpoch {
			m.mu.Unlock()
			return result, Fail("E_CANCELLED", "已停止设备接收等待")
		}
		if len(entry.queue) > 0 || entry.State != "connected" {
			count := min(len(entry.queue), 16)
			result.Packets = append(result.Packets, entry.queue[:count]...)
			for _, packet := range result.Packets {
				entry.bytes -= len(packet.Data)
			}
			entry.queue = entry.queue[count:]
			result.State = entry.State
			result.Error = entry.Error
			if len(entry.queue) > 0 {
				result.State = "connected"
				result.Error = nil
			}
			m.mu.Unlock()
			return result, nil
		}
		m.mu.Unlock()
		select {
		case <-entry.wake:
		case <-ctx.Done():
			return result, Fail("E_CANCELLED", "已取消接收等待")
		case <-timer.C:
			return DeviceRead{Packets: []DevicePacket{}, State: "connected"}, nil
		}
	}
}

// CancelRead 仅唤醒并停止当前接收等待，不关闭硬件或丢弃队列，供切换前端回调时安全交接读取权。
func (m *DeviceManager) CancelRead(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, err := m.ownedLocked(ctx, id)
	if err != nil {
		return err
	}
	entry.readEpoch++
	wakeDevice(entry)
	return nil
}

// Write 串行发送最多 64 KiB，等待最多五秒；取消或超时关闭连接，但不能撤回已经写入设备的部分字节。
func (m *DeviceManager) Write(ctx context.Context, id string, data []byte) (int, error) {
	if ctx.Err() != nil {
		return 0, Fail("E_CANCELLED", "设备发送请求已取消")
	}
	if len(data) == 0 || len(data) > 64<<10 {
		return 0, Fail("E_INVALID_ARGUMENT", "每次发送需要 1 字节至 64 KiB")
	}
	m.mu.Lock()
	entry, err := m.ownedLocked(ctx, id)
	if err != nil {
		m.mu.Unlock()
		return 0, err
	}
	if entry.State != "connected" || entry.writing {
		m.mu.Unlock()
		return 0, Fail("E_DEVICE_BUSY", "连接未就绪或正在发送")
	}
	entry.writing = true
	entry.seen = time.Now()
	m.mu.Unlock()
	guard, err := m.controller.BeginTask(ctx, "向设备发送数据")
	if err != nil {
		m.mu.Lock()
		entry.writing = false
		m.mu.Unlock()
		return 0, err
	}
	type answer struct {
		n   int
		err error
	}
	done := make(chan answer, 1)
	payload := append([]byte(nil), data...)
	// 退出保护由真实写入协程释放，RPC 取消等待不假装硬件已经停止，防止重启打断仍在进行的驱动调用。
	go func() {
		defer m.controller.EndTask(ctx, guard.ID)
		defer func() { m.mu.Lock(); entry.writing = false; m.mu.Unlock() }()
		total := 0
		for total < len(payload) {
			if ctx.Err() != nil {
				done <- answer{total, ctx.Err()}
				return
			}
			n, e := entry.port.Write(payload[total:])
			total += n
			if e != nil || n == 0 {
				if e == nil {
					e = io.ErrShortWrite
				}
				done <- answer{total, e}
				return
			}
		}
		done <- answer{total, nil}
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case result := <-done:
		if result.err != nil {
			m.stopEntry(entry, "failed", &Error{Code: "E_DEVICE_WRITE", Message: "发送失败，可能已经发送部分数据，请勿盲目重试"})
			return result.n, Fail("E_DEVICE_WRITE", "发送失败，可能已发送部分数据")
		}
		return result.n, nil
	case <-ctx.Done():
		m.stopEntry(entry, "closed", nil)
		return 0, Fail("E_CANCELLED", "已取消发送等待，可能已发送部分数据")
	case <-timer.C:
		m.stopEntry(entry, "failed", &Error{Code: "E_TIMEOUT", Message: "设备发送超时，可能已发送部分数据"})
		return 0, Fail("E_TIMEOUT", "设备发送超时，连接已关闭")
	}
}

// stopEntry 在锁内更新状态、锁外关闭驱动，避免 Close 与 Read 的系统调用相互卡住管理器。
func (m *DeviceManager) stopEntry(entry *deviceEntry, state string, failure *Error) {
	m.mu.Lock()
	if entry.State != "connected" && entry.State != "opening" {
		m.mu.Unlock()
		return
	}
	entry.State = state
	entry.Error = failure
	port := entry.port
	wakeDevice(entry)
	m.mu.Unlock()
	if port != nil {
		_ = port.Close()
	}
}

// Close 关闭本窗口的连接；终态重复关闭安全，可选移除记录释放历史配额。
func (m *DeviceManager) Close(ctx context.Context, id string, remove bool) error {
	m.mu.Lock()
	entry, err := m.ownedLocked(ctx, id)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	m.stopEntry(entry, "closed", nil)
	if remove {
		m.mu.Lock()
		delete(m.entries, id)
		m.mu.Unlock()
	}
	return nil
}

// ListConnections 只返回当前窗口仍有能力管理的连接，不为列表轮询续租设备会话。
func (m *DeviceManager) ListConnections(ctx context.Context) ([]DeviceConnection, error) {
	if Require(ctx, "devices.serial") != nil && Require(ctx, "devices.network") != nil {
		return nil, Fail("E_PERMISSION_DENIED", "缺少串口或网络设备权限")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	values := []DeviceConnection{}
	for _, entry := range m.entries {
		if entry.owner == resourceOwner(ctx) && Require(ctx, deviceCapability(entry.Kind)) == nil {
			values = append(values, entry.DeviceConnection)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return values, nil
}

// expire 清理超过一分钟未读取或发送的连接，SDK 正常轮询会持续续租。
func (m *DeviceManager) expire() {
	m.mu.Lock()
	values := []*deviceEntry{}
	for _, entry := range m.entries {
		if time.Since(entry.seen) > time.Minute {
			values = append(values, entry)
		}
	}
	m.mu.Unlock()
	for _, entry := range values {
		m.stopEntry(entry, "disconnected", &Error{Code: "E_DEVICE_EXPIRED", Message: "设备会话已失联，请重新连接"})
	}
}

// CloseWindow 关闭并移除真实窗口的全部设备，防止同名新窗口继承旧连接。
func (m *DeviceManager) CloseWindow(window string) {
	m.mu.Lock()
	values := []*deviceEntry{}
	for id, entry := range m.entries {
		if entry.window == window {
			values = append(values, entry)
			delete(m.entries, id)
		}
	}
	m.mu.Unlock()
	for _, entry := range values {
		m.stopEntry(entry, "closed", nil)
	}
}

// Shutdown 禁止新连接并关闭所有驱动；不强行撤回已经交给打印后台的真实打印作业。
func (m *DeviceManager) Shutdown() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.stop)
	values := []*deviceEntry{}
	for _, entry := range m.entries {
		values = append(values, entry)
	}
	m.mu.Unlock()
	for _, entry := range values {
		m.stopEntry(entry, "closed", nil)
	}
}

// ListPrinters 返回系统已安装打印机；当前 RAW 打印仅 Windows 实现，不触发打印或探测硬件。
func (m *DeviceManager) ListPrinters(ctx context.Context) ([]string, error) {
	if err := Require(ctx, "devices.print"); err != nil {
		return nil, err
	}
	return devicePrinters()
}

// PrintRaw 向已安装打印机发送设备语言，返回后台作业 ID，不代表已出纸；操作系统打印调用期间持续退出保护。
func (m *DeviceManager) PrintRaw(ctx context.Context, printer, title string, data []byte) (uint32, error) {
	printers, err := m.ListPrinters(ctx)
	if err != nil {
		return 0, err
	}
	if !slices.Contains(printers, printer) {
		return 0, Fail("E_DEVICE_NOT_FOUND", "打印机不在已安装列表中")
	}
	if len(data) == 0 || len(data) > 64<<10 || len([]rune(title)) > 120 {
		return 0, Fail("E_INVALID_ARGUMENT", "打印数据需要 1 字节至 64 KiB，标题最多 120 字")
	}
	if ctx.Err() != nil {
		return 0, Fail("E_CANCELLED", "打印请求已取消")
	}
	select {
	case m.printJobs <- struct{}{}:
		defer func() { <-m.printJobs }()
	default:
		return 0, Fail("E_DEVICE_BUSY", "最多同时提交两项 RAW 打印")
	}
	guard, err := m.controller.BeginTask(ctx, "提交标签打印")
	if err != nil {
		return 0, err
	}
	defer m.controller.EndTask(ctx, guard.ID)
	return devicePrintRaw(printer, title, data)
}
