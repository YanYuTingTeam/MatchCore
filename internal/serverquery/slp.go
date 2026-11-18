package serverquery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type TCPClient struct{ Timeout time.Duration }

var colors = regexp.MustCompile(`§[0-9a-zA-Z]`)

func (client TCPClient) Query(ctx context.Context, target Target) (Status, error) {
	host, portText, err := net.SplitHostPort(target.Address)
	if err != nil {
		return Status{}, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return Status{}, errors.New("SLP端口无效")
	}
	wait := client.Timeout
	if wait <= 0 {
		wait = 2 * time.Second
	}
	conn, err := (&net.Dialer{Timeout: wait}).DialContext(ctx, "tcp", target.Address)
	if err != nil {
		return Status{}, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline := time.Now().Add(wait)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return Status{}, err
	}
	var packet bytes.Buffer
	putInt(&packet, 0)
	putInt(&packet, 47)
	putInt(&packet, len(host))
	packet.WriteString(host)
	packet.WriteByte(byte(port >> 8))
	packet.WriteByte(byte(port))
	putInt(&packet, 1)
	if err = sendPacket(conn, packet.Bytes()); err != nil {
		return Status{}, err
	}
	if err = sendPacket(conn, []byte{0}); err != nil {
		return Status{}, err
	}
	size, err := getInt(conn)
	if err != nil || size <= 0 || size > 1024*1024 {
		return Status{}, errors.New("SLP包长度无效")
	}
	data := make([]byte, size)
	if _, err = io.ReadFull(conn, data); err != nil {
		return Status{}, err
	}
	buf := bytes.NewReader(data)
	id, err := getInt(buf)
	if err != nil || id != 0 {
		return Status{}, errors.New("SLP包类型无效")
	}
	length, err := getInt(buf)
	if err != nil || length != buf.Len() {
		return Status{}, errors.New("SLP响应长度无效")
	}
	var reply struct {
		Players *struct {
			Online *int `json:"online"`
			Max    *int `json:"max"`
		} `json:"players"`
		Description json.RawMessage `json:"description"`
	}
	if err = json.Unmarshal(data[len(data)-length:], &reply); err != nil || reply.Players == nil || reply.Players.Online == nil || reply.Players.Max == nil || *reply.Players.Online < 0 || *reply.Players.Online > 2147483647 || *reply.Players.Max < 0 || *reply.Players.Max > 2147483647 {
		return Status{}, errors.New("SLP状态无效")
	}
	motd, err := motdText(reply.Description, 0)
	if err != nil || len(motd) > 16*1024 {
		return Status{}, errors.New("SLP文字无效")
	}
	return Status{Online: true, PlayerCount: *reply.Players.Online, MaxPlayers: *reply.Players.Max, MOTD: colors.ReplaceAllString(motd, ""), CollectedAt: time.Now()}, nil
}

func sendPacket(writer io.Writer, data []byte) error {
	var packet bytes.Buffer
	putInt(&packet, len(data))
	packet.Write(data)
	_, err := packet.WriteTo(writer)
	return err
}
func putInt(writer *bytes.Buffer, value int) {
	for value >= 128 {
		writer.WriteByte(byte(value&127) | 128)
		value >>= 7
	}
	writer.WriteByte(byte(value))
}
func getInt(reader io.Reader) (int, error) {
	value := 0
	var part [1]byte
	for i := 0; i < 5; i++ {
		if _, err := io.ReadFull(reader, part[:]); err != nil {
			return 0, err
		}
		if i == 4 && part[0] > 7 {
			return 0, errors.New("SLP VarInt无效")
		}
		value |= int(part[0]&127) << (7 * i)
		if part[0]&128 == 0 {
			return value, nil
		}
	}
	return 0, errors.New("SLP VarInt过长")
}
func motdText(data json.RawMessage, depth int) (string, error) {
	if depth > 32 || len(data) == 0 {
		return "", errors.New("SLP文字深度无效")
	}
	var text string
	if json.Unmarshal(data, &text) == nil {
		return text, nil
	}
	var list []json.RawMessage
	if json.Unmarshal(data, &list) == nil && list != nil {
		var out strings.Builder
		for _, part := range list {
			text, err := motdText(part, depth+1)
			if err != nil {
				return "", err
			}
			out.WriteString(text)
		}
		return out.String(), nil
	}
	var object struct {
		Text  string            `json:"text"`
		Extra []json.RawMessage `json:"extra"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString(object.Text)
	for _, part := range object.Extra {
		text, err := motdText(part, depth+1)
		if err != nil {
			return "", err
		}
		out.WriteString(text)
	}
	return out.String(), nil
}
