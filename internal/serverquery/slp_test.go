package serverquery

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestSLPRealTCP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		for range 2 {
			size, err := getInt(conn)
			if err != nil {
				done <- err
				return
			}
			data := make([]byte, size)
			if _, err = io.ReadFull(conn, data); err != nil {
				done <- err
				return
			}
		}
		text := `{"players":{"online":3,"max":8},"description":{"text":"§aState: 1","extra":[{"text":"\nMap: arena1"}]}}`
		var packet bytes.Buffer
		putInt(&packet, 0)
		putInt(&packet, len(text))
		packet.WriteString(text)
		var wire bytes.Buffer
		putInt(&wire, packet.Len())
		wire.Write(packet.Bytes())
		for _, part := range wire.Bytes() {
			if _, err = conn.Write([]byte{part}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	status, err := (TCPClient{Timeout: time.Second}).Query(context.Background(), Target{Address: listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Online || status.PlayerCount != 3 || status.MaxPlayers != 8 || status.MOTD != "State: 1\nMap: arena1" {
		t.Fatal("SLP完整读取或MOTD格式异常")
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSLPRejectsOversizedPacket(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var packet bytes.Buffer
		putInt(&packet, 2*1024*1024)
		_, _ = packet.WriteTo(conn)
	}()
	if _, err = (TCPClient{Timeout: 100 * time.Millisecond}).Query(context.Background(), Target{Address: listener.Addr().String()}); err == nil {
		t.Fatal("超大SLP包未拒绝")
	}
}

func TestSLPCancel(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	end := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		<-end
	}()
	defer close(end)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err = (TCPClient{Timeout: time.Second}).Query(ctx, Target{Address: listener.Addr().String()}); err == nil || time.Since(start) > 500*time.Millisecond {
		t.Fatal("SLP取消未生效")
	}
}

func TestSLPPlayerBounds(t *testing.T) {
	for _, item := range []struct {
		name, online, max string
		want              bool
	}{
		{"zero", "0", "0", true},
		{"max", "2147483647", "2147483647", true},
		{"online_overflow", "2147483648", "2147483647", false},
		{"max_overflow", "1", "2147483648", false},
	} {
		t.Run(item.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				for range 2 {
					size, err := getInt(conn)
					if err != nil {
						done <- err
						return
					}
					if _, err = io.CopyN(io.Discard, conn, int64(size)); err != nil {
						done <- err
						return
					}
				}
				text := `{"players":{"online":` + item.online + `,"max":` + item.max + `},"description":"State: 1"}`
				var packet bytes.Buffer
				putInt(&packet, 0)
				putInt(&packet, len(text))
				packet.WriteString(text)
				done <- sendPacket(conn, packet.Bytes())
			}()
			status, err := (TCPClient{Timeout: time.Second}).Query(context.Background(), Target{Address: listener.Addr().String()})
			if serverErr := <-done; serverErr != nil {
				t.Fatal(serverErr)
			}
			if (err == nil) != item.want || status.Online != item.want {
				t.Fatal("SLP人数边界接受或拒绝异常")
			}
		})
	}
}
