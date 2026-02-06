package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

var logWriter io.Writer = os.Stdout

func main() {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println("==== TCP 测试工具 ====")
		fmt.Println("1) TCP 客户端")
		fmt.Println("2) TCP 服务端")
		fmt.Println("q) 退出")
		fmt.Print("请选择: ")

		choice, err := readLine(reader)
		if err != nil {
			fmt.Printf("输入错误: %v\n", err)
			return
		}

		switch strings.ToLower(choice) {
		case "1":
			runClient(reader)
		case "2":
			runServer(reader)
		case "q", "quit", "exit":
			return
		default:
			fmt.Println("无效选择")
		}
	}
}

func runClient(reader *bufio.Reader) {
	closeLog := setupLogger("client")
	defer closeLog()

	fmt.Println("选择服务端地址:")
	fmt.Println("1) 10.41.100.54:5056")
	fmt.Println("2) 10.170.0.96:5056")
	fmt.Println("3) 自定义配置")
	fmt.Print("请选择: ")

	choice, err := readLine(reader)
	if err != nil {
		logPrintf("输入错误: %v\n", err)
		return
	}

	var addr string
	switch strings.TrimSpace(choice) {
	case "1":
		addr = "10.41.100.54:5056"
	case "2":
		addr = "10.170.0.96:5056"
	default:
		fmt.Print("服务端 IP: ")
		ip, err := readLine(reader)
		if err != nil {
			logPrintf("输入错误: %v\n", err)
			return
		}
		if ip == "" {
			logPrintln("IP 不能为空")
			return
		}

		fmt.Print("服务端端口: ")
		port, err := readLine(reader)
		if err != nil {
			logPrintf("输入错误: %v\n", err)
			return
		}
		if port == "" {
			logPrintln("端口不能为空")
			return
		}
		addr = net.JoinHostPort(ip, port)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		logPrintln("\n收到信号，正在关闭客户端...")
		cancel()
		_ = conn.Close()
	}()

	var sendCount uint64
	var recvCount uint64

	for {
		if ctx.Err() != nil {
			return
		}

		logPrintf("尝试连接 %s ...\n", addr)
		conn, err := dialTCP(ctx, addr)
		if err != nil {
			logErrorDetail("连接失败", err)
			if !sleepWithContext(ctx, 3*time.Second) {
				return
			}
			continue
		}

		logPrintf("已连接到 %s，本地地址 %s\n", addr, conn.LocalAddr().String())
		err = clientSession(ctx, conn, &sendCount, &recvCount)
		_ = conn.Close()

		if ctx.Err() != nil {
			return
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				logPrintln("服务端已断开连接")
			} else {
				logErrorDetail("连接中断", err)
			}
		} else {
			logPrintln("连接已结束")
		}

		logPrintln("3 秒后自动重连...")
		if !sleepWithContext(ctx, 3*time.Second) {
			return
		}
	}
}

func runServer(reader *bufio.Reader) {
	closeLog := setupLogger("server")
	defer closeLog()

	fmt.Print("监听 IP（回车默认 0.0.0.0）: ")
	ip, err := readLine(reader)
	if err != nil {
		logPrintf("输入错误: %v\n", err)
		return
	}
	if ip == "" {
		ip = "0.0.0.0"
	}

	fmt.Print("监听端口（回车默认 5056）: ")
	port, err := readLine(reader)
	if err != nil {
		logPrintf("输入错误: %v\n", err)
		return
	}
	if port == "" {
		port = "5056"
	}

	addr := net.JoinHostPort(ip, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logPrintf("监听失败: %v\n", err)
		return
	}
	defer ln.Close()

	logPrintf("正在监听 %s\n", addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		logPrintln("\n收到信号，正在关闭服务端...")
		cancel()
		_ = ln.Close()
	}()

	var sendCount uint64
	var recvCount uint64

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			logPrintf("接收连接失败: %v\n", err)
			continue
		}

		go handleConn(conn, &sendCount, &recvCount)
	}
}

func handleConn(conn net.Conn, sendCount, recvCount *uint64) {
	defer conn.Close()

	remote := conn.RemoteAddr().String()
	logPrintf("客户端已连接: %s\n", remote)

	buf := bufio.NewReader(conn)
	for {
		line, err := buf.ReadString('\n')
		if err != nil {
			if !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
				logPrintf("[%s] 读取失败: %v\n", remote, err)
			}
			logPrintf("客户端已断开: %s\n", remote)
			return
		}
		recv := atomic.AddUint64(recvCount, 1)
		logPrintf("[%s] 接收 #%d: %s", remote, recv, line)

		msg := time.Now().Format(time.RFC3339Nano)
		_, err = fmt.Fprintf(conn, "%s\n", msg)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				logPrintf("[%s] 发送失败: %v\n", remote, err)
			}
			return
		}
		send := atomic.AddUint64(sendCount, 1)
		logPrintf("[%s] 发送 #%d: %s\n", remote, send, msg)
	}
}

func clientSession(ctx context.Context, conn net.Conn, sendCount, recvCount *uint64) error {
	errCh := make(chan error, 1)

	go func() {
		buf := bufio.NewReader(conn)
		for {
			line, err := buf.ReadString('\n')
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
			count := atomic.AddUint64(recvCount, 1)
			logPrintf("接收 #%d: %s", count, line)
		}
	}()

	send := func() error {
		msg := time.Now().Format(time.RFC3339Nano)
		_, err := fmt.Fprintf(conn, "%s\n", msg)
		if err != nil {
			return err
		}
		count := atomic.AddUint64(sendCount, 1)
		logPrintf("发送 #%d: %s\n", count, msg)
		return nil
	}

	if err := send(); err != nil {
		return err
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			return err
		case <-ticker.C:
			if err := send(); err != nil {
				return err
			}
		}
	}
}

func dialTCP(ctx context.Context, addr string) (net.Conn, error) {
	dialer := net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 5 * time.Second,
	}
	return dialer.DialContext(ctx, "tcp", addr)
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func logErrorDetail(prefix string, err error) {
	logPrintf("%s: %v\n", prefix, err)

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		logPrintf("  详细: op=%s net=%s addr=%v timeout=%v temporary=%v\n",
			opErr.Op, opErr.Net, opErr.Addr, opErr.Timeout(), opErr.Temporary())
		if opErr.Err != nil {
			logPrintf("  原因: %v\n", opErr.Err)
		}
	}

	var sysErr *os.SyscallError
	if errors.As(err, &sysErr) {
		logPrintf("  系统: syscall=%s errno=%v\n", sysErr.Syscall, sysErr.Err)
	}
}

func setupLogger(role string) func() {
	ts := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("tcptest-%s-%s.log", role, ts)
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stdout, "日志文件创建失败: %v\n", err)
		logWriter = os.Stdout
		return func() {}
	}
	logWriter = io.MultiWriter(os.Stdout, file)
	logPrintf("日志文件: %s\n", filename)
	logPrintf("启动时间: %s\n", time.Now().Format(time.RFC3339Nano))
	return func() {
		_ = file.Close()
	}
}

func logPrintln(args ...interface{}) {
	_, _ = fmt.Fprintln(logWriter, args...)
}

func logPrintf(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(logWriter, format, args...)
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && len(line) > 0 {
			return strings.TrimSpace(line), nil
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}
