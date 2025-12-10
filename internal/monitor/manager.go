package monitor

import (
	"fmt"
	"log"
	"sync"

	"log-filter-monitor/internal/filter"
)

// MultiMonitor 多文件监控管理器
// 负责管理多个日志文件的监控
type MultiMonitor struct {
	monitors map[string]*LogMonitor // 文件路径 -> 监控器映射
	wg       sync.WaitGroup         // 等待组
	outputChan chan filter.LogLineWithFile // 统一输出通道
	stopChan  chan struct{}         // 停止信号通道
}

// NewMultiMonitor 创建多文件监控管理器
// 返回: MultiMonitor实例
func NewMultiMonitor() *MultiMonitor {
	// 增加通道缓冲大小，提高并发性能
	const outputChanSize = 500 // 输出通道缓冲大小（增加到500）
	
	return &MultiMonitor{
		monitors:   make(map[string]*LogMonitor),
		outputChan: make(chan filter.LogLineWithFile, outputChanSize),
		stopChan:   make(chan struct{}),
	}
}

// AddMonitor 添加监控器
// filePath: 要监控的文件路径
// 返回: 错误信息（如果有）
func (mm *MultiMonitor) AddMonitor(filePath string) error {
	// 如果已经存在该文件的监控器，跳过
	if _, exists := mm.monitors[filePath]; exists {
		log.Printf("警告：文件 %s 已经在监控中，跳过\n", filePath)
		return nil
	}

	monitor := NewLogMonitor(filePath)
	if err := monitor.Start(); err != nil {
		return fmt.Errorf("启动监控文件 %s 失败: %w", filePath, err)
	}

	mm.monitors[filePath] = monitor

	// 启动goroutine转发日志行
	mm.wg.Add(1)
	go mm.forwardLogs(monitor, filePath)

	log.Printf("已添加监控文件: %s\n", filePath)
	return nil
}

// forwardLogs 转发日志行，添加文件路径信息
// monitor: 监控器
// filePath: 文件路径
func (mm *MultiMonitor) forwardLogs(monitor *LogMonitor, filePath string) {
	defer mm.wg.Done()

	for {
		select {
		case <-mm.stopChan:
			return
		case logLine, ok := <-monitor.LogChan:
			if !ok {
				// 通道已关闭
				return
			}

			// 发送带文件信息的日志行
			select {
			case mm.outputChan <- filter.LogLineWithFile{
				LogLine: logLine,
				LogFile: filePath,
			}:
			case <-mm.stopChan:
				return
			}
		}
	}
}

// GetOutputChan 获取输出通道
// 返回: 输出通道
func (mm *MultiMonitor) GetOutputChan() <-chan filter.LogLineWithFile {
	return mm.outputChan
}

// Stop 停止所有监控器
func (mm *MultiMonitor) Stop() {
	close(mm.stopChan)

	// 停止所有监控器
	for filePath, monitor := range mm.monitors {
		monitor.Stop()
		log.Printf("已停止监控文件: %s\n", filePath)
	}

	mm.wg.Wait()
	close(mm.outputChan)
}

// GetMonitoredFiles 获取所有监控的文件路径
// 返回: 文件路径列表
func (mm *MultiMonitor) GetMonitoredFiles() []string {
	files := make([]string, 0, len(mm.monitors))
	for filePath := range mm.monitors {
		files = append(files, filePath)
	}
	return files
}

