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
	fmt.Println("选择服务端地址:")
	fmt.Println("1) 10.41.100.54:5056")
	fmt.Println("2) 10.170.0.96:5056")
	fmt.Println("3) 自定义配置")
	fmt.Print("请选择: ")

	choice, err := readLine(reader)
	if err != nil {
		fmt.Printf("输入错误: %v\n", err)
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
			fmt.Printf("输入错误: %v\n", err)
			return
		}
		if ip == "" {
			fmt.Println("IP 不能为空")
			return
		}

		fmt.Print("服务端端口: ")
		port, err := readLine(reader)
		if err != nil {
			fmt.Printf("输入错误: %v\n", err)
			return
		}
		if port == "" {
			fmt.Println("端口不能为空")
			return
		}
		addr = net.JoinHostPort(ip, port)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Printf("连接失败: %v\n", err)
		return
	}
	defer conn.Close()

	fmt.Printf("已连接到 %s\n", addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		fmt.Println("\n收到信号，正在关闭客户端...")
		cancel()
		_ = conn.Close()
	}()

	var sendCount uint64
	var recvCount uint64

	go func() {
		buf := bufio.NewReader(conn)
		for {
			line, err := buf.ReadString('\n')
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					fmt.Printf("读取失败: %v\n", err)
				}
				cancel()
				return
			}
			count := atomic.AddUint64(&recvCount, 1)
			fmt.Printf("接收 #%d: %s", count, line)
		}
	}()

	send := func() bool {
		msg := time.Now().Format(time.RFC3339Nano)
		_, err := fmt.Fprintf(conn, "%s\n", msg)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				fmt.Printf("发送失败: %v\n", err)
			}
			cancel()
			return false
		}
		count := atomic.AddUint64(&sendCount, 1)
		fmt.Printf("发送 #%d: %s\n", count, msg)
		return true
	}

	send()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !send() {
				return
			}
		}
	}
}

func runServer(reader *bufio.Reader) {
	fmt.Print("监听 IP（回车默认 0.0.0.0）: ")
	ip, err := readLine(reader)
	if err != nil {
		fmt.Printf("输入错误: %v\n", err)
		return
	}
	if ip == "" {
		ip = "0.0.0.0"
	}

	fmt.Print("监听端口（回车默认 5056）: ")
	port, err := readLine(reader)
	if err != nil {
		fmt.Printf("输入错误: %v\n", err)
		return
	}
	if port == "" {
		port = "5056"
	}

	addr := net.JoinHostPort(ip, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("监听失败: %v\n", err)
		return
	}
	defer ln.Close()

	fmt.Printf("正在监听 %s\n", addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		fmt.Println("\n收到信号，正在关闭服务端...")
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
			fmt.Printf("接收连接失败: %v\n", err)
			continue
		}

		go handleConn(conn, &sendCount, &recvCount)
	}
}

func handleConn(conn net.Conn, sendCount, recvCount *uint64) {
	defer conn.Close()

	remote := conn.RemoteAddr().String()
	fmt.Printf("客户端已连接: %s\n", remote)

	buf := bufio.NewReader(conn)
	for {
		line, err := buf.ReadString('\n')
		if err != nil {
			if !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
				fmt.Printf("[%s] 读取失败: %v\n", remote, err)
			}
			fmt.Printf("客户端已断开: %s\n", remote)
			return
		}
		recv := atomic.AddUint64(recvCount, 1)
		fmt.Printf("[%s] 接收 #%d: %s", remote, recv, line)

		msg := time.Now().Format(time.RFC3339Nano)
		_, err = fmt.Fprintf(conn, "%s\n", msg)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				fmt.Printf("[%s] 发送失败: %v\n", remote, err)
			}
			return
		}
		send := atomic.AddUint64(sendCount, 1)
		fmt.Printf("[%s] 发送 #%d: %s\n", remote, send, msg)
	}
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
