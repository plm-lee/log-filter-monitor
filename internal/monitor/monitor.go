package monitor

import (
	"fmt"
	"log"
	"sync"

	"github.com/hpcloud/tail"
)

// LineWithOffset 带偏移量的日志行（用于断点续传）
type LineWithOffset struct {
	Text   string
	Offset int64
}

// LogMonitor 日志监控器结构体
// 负责实时监控日志文件并读取日志行
type LogMonitor struct {
	filePath    string              // 要监控的日志文件路径
	tail        *tail.Tail          // tail实例
	stopChan    chan struct{}       // 停止信号通道
	wg          sync.WaitGroup      // 等待组，用于优雅关闭
	LogChan     chan LineWithOffset // 日志行通道，用于输出读取到的日志行及偏移
	initialSeek int64               // 启动时的起始偏移，-1 表示从末尾
}

// NewLogMonitor 创建新的日志监控器实例
// filePath: 要监控的文件路径
// initialOffset: 起始偏移量，>=0 时从该位置继续读取（断点续传），<0 时从文件末尾开始
func NewLogMonitor(filePath string, initialOffset int64) *LogMonitor {
	const logChanSize = 500
	return &LogMonitor{
		filePath:    filePath,
		stopChan:    make(chan struct{}),
		LogChan:     make(chan LineWithOffset, logChanSize),
		initialSeek: initialOffset,
	}
}

// Start 启动日志监控
// 开始实时监控日志文件并通过 LogChan 通道发送日志行
// 返回: 错误信息（如果有）
func (lm *LogMonitor) Start() error {
	loc := &tail.SeekInfo{Offset: 0, Whence: 2}
	if lm.initialSeek >= 0 {
		loc = &tail.SeekInfo{Offset: lm.initialSeek, Whence: 0}
		log.Printf("从断点续传，文件: %s 偏移: %d\n", lm.filePath, lm.initialSeek)
	}
	config := tail.Config{
		Follow:    true,
		ReOpen:    true,
		MustExist: false,
		Poll:      true,
		Location:  loc,
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
// 从tail.Lines通道读取日志行，并维护字节偏移量用于断点续传
func (lm *LogMonitor) readLines() {
	defer lm.wg.Done()
	defer close(lm.LogChan)

	var offset int64
	if lm.initialSeek >= 0 {
		offset = lm.initialSeek
	}

	for {
		select {
		case <-lm.stopChan:
			return
		case line, ok := <-lm.tail.Lines:
			if !ok {
				return
			}
			if line.Err != nil {
				log.Printf("读取日志行时出错: %v\n", line.Err)
				continue
			}
			// 计算本行后的偏移（行内容 + 换行符）
			lineLen := int64(len(line.Text)) + 1
			select {
			case lm.LogChan <- LineWithOffset{Text: line.Text, Offset: offset}:
				offset += lineLen
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
