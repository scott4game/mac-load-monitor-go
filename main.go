package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

func main() {
	os.Exit(run())
}

func run() int {
	envPath := flag.String("env", ".env", "配置文件路径")
	once := flag.Bool("once", false, "采样一次并输出，不推送")
	testAlert := flag.Bool("test-alert", false, "发送一条飞书测试消息")
	flag.Parse()

	logger := log.New(os.Stderr, "", log.Ldate|log.Ltime|log.Lmicroseconds)
	if runtime.GOOS != "darwin" {
		logger.Printf("ERROR 该程序仅支持 macOS")
		return 2
	}
	config, err := LoadConfig(*envPath)
	if err != nil {
		logger.Printf("ERROR 启动失败: %v", err)
		return 2
	}
	if !*once {
		if err := config.ValidateWebhook(); err != nil {
			logger.Printf("ERROR 启动失败: %v", err)
			return 2
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	collector := NewSystemCollector()
	if *once {
		sample := collector.Collect(ctx)
		collector.AddTopProcesses(ctx, &sample, config.TopProcessCount)
		fmt.Println(FormatMessage(config, sample, "当前状态", EvaluateBreaches(config, sample)))
		return 0
	}
	notifier := NewFeishuNotifier(config, logger)
	if *testAlert {
		sample := collector.Collect(ctx)
		collector.AddTopProcesses(ctx, &sample, config.TopProcessCount)
		if notifier.Send(ctx, FormatMessage(config, sample, "测试消息", EvaluateBreaches(config, sample))) {
			return 0
		}
		return 1
	}
	monitor := NewMonitor(config, collector, notifier, logger)
	if err := monitor.Run(ctx); err != nil {
		logger.Printf("ERROR 监控异常退出: %v", err)
		return 1
	}
	return 0
}
