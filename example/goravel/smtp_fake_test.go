package main

import (
	"bufio"
	"net"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeSMTP is a tiny mail server that accepts any login. Goravel's mailer
// always sends AUTH, which a plain Mailpit refuses, so the test brings its
// own. It counts connections so a runaway retry loop can be stopped at once.
type fakeSMTP struct {
	listener net.Listener
	conns    atomic.Int64
	messages atomic.Int64
	onConn   func(total int64)
}

func startFakeSMTP(t *testing.T, addr string, onConn func(total int64)) *fakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot listen on %s: %v", addr, err)
	}

	server := &fakeSMTP{listener: listener, onConn: onConn}
	t.Cleanup(func() { _ = listener.Close() })

	go server.accept()

	return server
}

func (s *fakeSMTP) accept() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}

		total := s.conns.Add(1)
		if s.onConn != nil {
			s.onConn(total)
		}

		go s.serve(conn)
	}
}

func (s *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	write("220 fake ESMTP")

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		command := strings.ToUpper(strings.TrimSpace(line))

		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			write("250-fake")
			write("250 AUTH LOGIN PLAIN")
		case strings.HasPrefix(command, "AUTH LOGIN"):
			write("334 UGFzc3dvcmQ6")
			_, _ = reader.ReadString('\n')
			write("235 ok")
		case strings.HasPrefix(command, "AUTH"):
			write("235 ok")
		case strings.HasPrefix(command, "DATA"):
			write("354 go on")
			s.readData(reader)
			s.messages.Add(1)
			write("250 queued")
		case strings.HasPrefix(command, "QUIT"):
			write("221 bye")

			return
		default:
			write("250 ok")
		}
	}
}

func (s *fakeSMTP) readData(reader *bufio.Reader) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "." {
			return
		}
	}
}
