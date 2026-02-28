package handler

import (
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// ReportStatsRecorder 上报统计记录器接口
// 用于记录每次上报的耗时和数量
type ReportStatsRecorder interface {
	RecordReport(duration time.Duration, count int64)
}

// ReportStatsCollector 上报统计收集器
// 每分钟聚合：累计上报数量、平均接口耗时，并输出日志
type ReportStatsCollector struct {
	totalDurationNanos int64 // 累计耗时（纳秒）
	totalCount         int64 // 累计上报条数
	callCount          int64 // 上报接口调用次数

	interval  time.Duration
	stopChan  chan struct{}
	wg        sync.WaitGroup
	started   int32
}

// NewReportStatsCollector 创建上报统计收集器
// interval: 统计输出间隔（默认 1 分钟）
func NewReportStatsCollector(interval time.Duration) *ReportStatsCollector {
	if interval <= 0 {
		interval = time.Minute
	}
	return &ReportStatsCollector{
		interval: interval,
		stopChan: make(chan struct{}),
	}
}

// RecordReport 记录一次上报
// duration: 本次上报接口耗时
// count: 本次上报的日志条数
func (r *ReportStatsCollector) RecordReport(duration time.Duration, count int64) {
	if r == nil {
		return
	}
	atomic.AddInt64(&r.totalDurationNanos, int64(duration))
	atomic.AddInt64(&r.totalCount, count)
	atomic.AddInt64(&r.callCount, 1)
}

// GetAndReset 获取本周期统计并重置
// 返回: totalCount, avgDurationMs, callCount
func (r *ReportStatsCollector) GetAndReset() (totalCount int64, avgDurationMs float64, callCount int64) {
	if r == nil {
		return 0, 0, 0
	}
	totalDuration := atomic.SwapInt64(&r.totalDurationNanos, 0)
	totalCount = atomic.SwapInt64(&r.totalCount, 0)
	callCount = atomic.SwapInt64(&r.callCount, 0)

	if callCount > 0 {
		avgDurationMs = float64(totalDuration) / float64(callCount) / 1e6
	}
	return totalCount, avgDurationMs, callCount
}

// Start 启动定期统计输出
func (r *ReportStatsCollector) Start() {
	if r == nil {
		return
	}
	if !atomic.CompareAndSwapInt32(&r.started, 0, 1) {
		return
	}
	r.wg.Add(1)
	go r.periodicLog()
}

// periodicLog 定期输出上报统计日志
func (r *ReportStatsCollector) periodicLog() {
	defer r.wg.Done()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopChan:
			return
		case <-ticker.C:
			totalCount, avgDurationMs, callCount := r.GetAndReset()
			// 仅当有上报时输出
			if totalCount > 0 || callCount > 0 {
				log.Printf("[上报统计] 本分钟累计上报数量: %d, 平均接口耗时: %.2fms, 接口调用次数: %d\n",
					totalCount, avgDurationMs, callCount)
			}
		}
	}
}

// Stop 停止统计收集器
func (r *ReportStatsCollector) Stop() {
	if r == nil {
		return
	}
	if atomic.LoadInt32(&r.started) != 1 {
		return
	}
	close(r.stopChan)
	r.wg.Wait()
}
