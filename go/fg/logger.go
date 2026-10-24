package fg

import (
	"fmt"
	"github.com/natefinch/lumberjack"
	"github.com/sirupsen/logrus"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

var (
	Logger *logrus.Logger
	once   = sync.Once{}
)

// init 初始化这套兼容日志工具使用的单例日志器；不会启动宿主管理或注册前端服务。
func init() {
	GetLogger()
}

type MyLogrus struct {
	logger *logrus.Logger
}

// getLogPath 按这套日志工具的历史约定选择日志根目录：macOS/Linux 使用用户主目录，其他系统使用当前工作目录。
func getLogPath() string {
	osName := runtime.GOOS
	//isWindows := osName == "windows"
	isLinux := osName == "linux"
	isMac := osName == "darwin"
	if isMac || isLinux {
		homeDir, _ := os.UserHomeDir()
		return homeDir
	}
	dir, _ := os.Getwd()
	return dir
}

// GetLogger 通过 sync.Once 创建单例日志器，配置日志轮转、格式及控制台输出，后续调用复用同一实例。
func GetLogger() *logrus.Logger {
	once.Do(func() {
		// 创建一个新的 logrus 实例
		Logger = logrus.New()

		// 设置日志级别
		Logger.SetLevel(logrus.InfoLevel)
		path := getLogPath()
		joinLogPath := filepath.Join(path, "log", "log.log")
		fmt.Println("log-path:" + joinLogPath)
		// 创建日志切割器
		logWriter := &lumberjack.Logger{
			Filename:   joinLogPath,
			MaxSize:    20,   // 单个日志文件最大大小（MB）
			MaxBackups: 3,    // 保留旧文件的最大数量
			MaxAge:     7,    // 保留旧文件的最大天数
			Compress:   true, // 压缩旧文件
		}

		writer := io.MultiWriter(logWriter, os.Stdout)

		// 设置日志输出为文件
		Logger.SetOutput(writer)

		// 自定义日志格式
		logFormatter := &logrus.TextFormatter{
			TimestampFormat:  time.DateTime,
			FullTimestamp:    true,
			ForceColors:      true,
			DisableColors:    false,
			PadLevelText:     true,
			QuoteEmptyFields: true,
			FieldMap: logrus.FieldMap{
				logrus.FieldKeyTime:  "@timestamp",
				logrus.FieldKeyMsg:   "message",
				logrus.FieldKeyLevel: "severity",
			},
		}
		Logger.SetFormatter(logFormatter)
	})

	return Logger
}

// InitLog 返回包装单例日志器的兼容日志对象，供旧代码通过统一方法写日志。
func InitLog() *MyLogrus {
	return &MyLogrus{logger: GetLogger()}
}

// Println 使用兼容日志器输出一条普通日志，并沿用底层 Println 的换行处理。
func (l *MyLogrus) Println(message string) {
	l.logger.Println(message)
}

// Trace 以 trace 级别记录调试细节；是否实际输出取决于日志器当前级别。
func (l *MyLogrus) Trace(message string) {
	l.logger.Trace(message)
}

// Debug 以 debug 级别记录排查信息；是否实际输出取决于日志器当前级别。
func (l *MyLogrus) Debug(message string) {
	l.logger.Debug(message)
}

// Info 以 info 级别记录正常运行过程中的消息。
func (l *MyLogrus) Info(message string) {
	l.logger.Info(message)
}

// Warning 以 warning 级别记录需要关注但未必导致操作失败的情况。
func (l *MyLogrus) Warning(message string) {
	l.logger.Warning(message)
}

// Error 以 error 级别记录操作失败或其他错误信息。
func (l *MyLogrus) Error(message string) {
	l.logger.Error(message)
}

// Fatal 为兼容旧调用，以 fatal 级别记录日志；底层 logrus 的 Fatal 会退出进程，即使本方法未显式调用 os.Exit。
func (l *MyLogrus) Fatal(message string) {
	l.logger.Fatal(message)
	//os.Exit(1)
}

// Print 使用兼容日志器输出普通日志，沿用底层 Print 的文本处理。
func (l *MyLogrus) Print(message string) {
	l.logger.Print(message)
}
