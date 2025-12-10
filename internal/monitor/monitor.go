package monitor

import (
	"fmt"
	"log"
	"sync"

	"github.com/hpcloud/tail"
)

// LogMonitor 日志监控器结构体
// 负责实时监控日志文件并读取日志行
type LogMonitor struct {
	filePath string         // 要监控的日志文件路径
	tail     *tail.Tail     // tail实例
	stopChan chan struct{}  // 停止信号通道
	wg       sync.WaitGroup // 等待组，用于优雅关闭
	LogChan  chan string    // 日志行通道，用于输出读取到的日志行
}

// NewLogMonitor 创建新的日志监控器实例
// filePath: 要监控的日志文件路径
// 返回: LogMonitor实例
func NewLogMonitor(filePath string) *LogMonitor {
	// 增加通道缓冲大小，提高并发性能
	const logChanSize = 500 // 日志通道缓冲大小（增加到500）

	return &LogMonitor{
		filePath: filePath,
		stopChan: make(chan struct{}),
		LogChan:  make(chan string, logChanSize), // 带缓冲的通道，避免阻塞
	}
}

// Start 启动日志监控
// 开始实时监控日志文件并通过 LogChan 通道发送日志行
// 返回: 错误信息（如果有）
func (lm *LogMonitor) Start() error {
	// 配置tail选项
	config := tail.Config{
		Follow:    true,                                 // 持续跟踪文件变化
		ReOpen:    true,                                 // 文件被移动或删除后重新打开（支持日志轮转）
		MustExist: false,                                // 文件不存在时不报错，等待文件创建
		Poll:      true,                                 // 使用轮询方式检测文件变化（兼容性更好）
		Location:  &tail.SeekInfo{Offset: 0, Whence: 2}, // 从文件末尾开始读取
	}

	// 打开文件进行监控
	t, err := tail.TailFile(lm.filePath, config)
	if err != nil {
		return fmt.Errorf("无法打开日志文件 %s: %w", lm.filePath, err)
	}

	lm.tail = t

	// 启动goroutine读取日志行
	lm.wg.Add(1)
	go lm.readLines()

	log.Printf("开始监控日志文件: %s\n", lm.filePath)

	return nil
}

// readLines 读取日志行的goroutine
// 从tail.Lines通道读取日志行，并通过 LogChan 发送出去
func (lm *LogMonitor) readLines() {
	defer lm.wg.Done()
	defer close(lm.LogChan)

	for {
		select {
		case <-lm.stopChan:
			return
		case line, ok := <-lm.tail.Lines:
			if !ok {
				// 通道已关闭
				return
			}
			if line.Err != nil {
				log.Printf("读取日志行时出错: %v\n", line.Err)
				continue
			}

			// 将日志行发送到通道
			select {
			case lm.LogChan <- line.Text:
			case <-lm.stopChan:
				return
			}
		}
	}
}

// Stop 停止日志监控
// 优雅地关闭tail实例和所有goroutine
func (lm *LogMonitor) Stop() {
	close(lm.stopChan)
	if lm.tail != nil {
		lm.tail.Stop()
	}
	lm.wg.Wait()
}
