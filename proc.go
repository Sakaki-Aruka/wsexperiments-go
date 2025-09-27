package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		return
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = os.Environ()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Println("failed to get stdout pipe")
		return
	}
	stdoutScanner := bufio.NewReader(stdout)
	cmd.Stdin = os.Stdin
	sig := make(chan os.Signal)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGKILL, os.Interrupt)
	done := make(chan struct{})

	if err := cmd.Start(); err != nil {
		fmt.Println("process start error: " + err.Error())
		return
	}

	inChannel := make(chan []byte)

	go func() {
		for {
			buf := make([]byte, 1024)
			n, err := stdoutScanner.Read(buf)
			if err != nil {
				fmt.Println("read error:", err)
				break
			}
			if n > 0 {
				d := buf[:n]
				inChannel <- d
				//fmt.Print(string(d))
			}
		}
		done <- struct{}{}
		close(done)
	}()

	go func() {
		for {
			select {
			case <-done:
				close(inChannel)
				return
			case d := <-inChannel:
				fmt.Print(string(d))
			}
		}
	}()

	go func() {
		select {
		case s := <-sig:
			if err := cmd.Process.Signal(s); err != nil {
				fmt.Println("kill error: " + err.Error())
			}
		}
	}()

	<-done
}
