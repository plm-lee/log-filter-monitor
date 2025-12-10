package main

import (
	"fmt"
	"os"
	"runtime"

	"log-filter-monitor/internal/app"
)

func main() {
	// 解析命令行参数
	configFile, logFile := app.ParseFlags()

	// 创建应用实例
	application := app.NewApp()

	// 初始化所有模块
	if err := application.InitAll(configFile, logFile); err != nil {
		fmt.Fprintf(os.Stderr, "初始化失败: %v\n", err)
		fmt.Fprintf(os.Stderr, "使用方法：\n")
		fmt.Fprintf(os.Stderr, "  -config string\n")
		fmt.Fprintf(os.Stderr, "        配置文件路径（可选，默认：config.yaml）\n")
		fmt.Fprintf(os.Stderr, "  -file string\n")
		fmt.Fprintf(os.Stderr, "        要监控的日志文件路径（可选，如果规则中配置了log_file则不需要）\n")
		os.Exit(1)
	}

	// 延迟关闭
	defer application.Stop()

	// 设置运行时参数
	// 使用所有可用的 CPU 核心
	runtime.GOMAXPROCS(runtime.NumCPU())

	// 启动所有服务
	application.Start()

	// 等待退出信号
	application.Wait()
}
