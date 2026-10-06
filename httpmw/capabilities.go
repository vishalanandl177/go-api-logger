package httpmw

import (
	"bufio"
	"io"
	"net"
	"net/http"
)

type flushCapability struct{ c *captureWriter }

func (f flushCapability) Flush() {
	f.c.commit(200)
	f.c.buffer.omit("streaming")
	f.c.original.(http.Flusher).Flush()
}

type hijackCapability struct{ c *captureWriter }

func (h hijackCapability) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := h.c.original.(http.Hijacker).Hijack()
	if err == nil {
		h.c.mu.Lock()
		h.c.hijacked = true
		h.c.mu.Unlock()
		h.c.buffer.omit("upgraded")
	}
	return conn, rw, err
}

type pushCapability struct{ c *captureWriter }

func (p pushCapability) Push(target string, options *http.PushOptions) error {
	return p.c.original.(http.Pusher).Push(target, options)
}

type readFromCapability struct{ c *captureWriter }

func (r readFromCapability) ReadFrom(src io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{r.c}, src)
}

type closeNotifyCapability struct{ c *captureWriter }

type flushErrorCapability struct{ c *captureWriter }

func (f flushErrorCapability) FlushError() error {
	f.c.commit(200)
	f.c.buffer.omit("streaming")
	return f.c.original.(interface{ FlushError() error }).FlushError()
}

func (n closeNotifyCapability) CloseNotify() <-chan bool {
	return n.c.original.(http.CloseNotifier).CloseNotify()
}

// wrapWriter exposes exactly the optional interfaces supported by the writer.
func wrapWriter(c *captureWriter) http.ResponseWriter {
	mask := 0
	if _, ok := c.original.(http.Flusher); ok {
		mask |= 1
	}
	if _, ok := c.original.(http.Hijacker); ok {
		mask |= 2
	}
	if _, ok := c.original.(http.Pusher); ok {
		mask |= 4
	}
	if _, ok := c.original.(io.ReaderFrom); ok {
		mask |= 8
	}
	if _, ok := c.original.(http.CloseNotifier); ok {
		mask |= 16
	}
	if _, ok := c.original.(interface{ FlushError() error }); ok {
		mask |= 32
	}
	switch mask {
	case 0:
		return struct{ *captureWriter }{c}
	case 1:
		return struct {
			*captureWriter
			flushCapability
		}{c, flushCapability{c}}
	case 2:
		return struct {
			*captureWriter
			hijackCapability
		}{c, hijackCapability{c}}
	case 3:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
		}{c, flushCapability{c}, hijackCapability{c}}
	case 4:
		return struct {
			*captureWriter
			pushCapability
		}{c, pushCapability{c}}
	case 5:
		return struct {
			*captureWriter
			flushCapability
			pushCapability
		}{c, flushCapability{c}, pushCapability{c}}
	case 6:
		return struct {
			*captureWriter
			hijackCapability
			pushCapability
		}{c, hijackCapability{c}, pushCapability{c}}
	case 7:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			pushCapability
		}{c, flushCapability{c}, hijackCapability{c}, pushCapability{c}}
	case 8:
		return struct {
			*captureWriter
			readFromCapability
		}{c, readFromCapability{c}}
	case 9:
		return struct {
			*captureWriter
			flushCapability
			readFromCapability
		}{c, flushCapability{c}, readFromCapability{c}}
	case 10:
		return struct {
			*captureWriter
			hijackCapability
			readFromCapability
		}{c, hijackCapability{c}, readFromCapability{c}}
	case 11:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			readFromCapability
		}{c, flushCapability{c}, hijackCapability{c}, readFromCapability{c}}
	case 12:
		return struct {
			*captureWriter
			pushCapability
			readFromCapability
		}{c, pushCapability{c}, readFromCapability{c}}
	case 13:
		return struct {
			*captureWriter
			flushCapability
			pushCapability
			readFromCapability
		}{c, flushCapability{c}, pushCapability{c}, readFromCapability{c}}
	case 14:
		return struct {
			*captureWriter
			hijackCapability
			pushCapability
			readFromCapability
		}{c, hijackCapability{c}, pushCapability{c}, readFromCapability{c}}
	case 15:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			pushCapability
			readFromCapability
		}{c, flushCapability{c}, hijackCapability{c}, pushCapability{c}, readFromCapability{c}}
	case 16:
		return struct {
			*captureWriter
			closeNotifyCapability
		}{c, closeNotifyCapability{c}}
	case 17:
		return struct {
			*captureWriter
			flushCapability
			closeNotifyCapability
		}{c, flushCapability{c}, closeNotifyCapability{c}}
	case 18:
		return struct {
			*captureWriter
			hijackCapability
			closeNotifyCapability
		}{c, hijackCapability{c}, closeNotifyCapability{c}}
	case 19:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			closeNotifyCapability
		}{c, flushCapability{c}, hijackCapability{c}, closeNotifyCapability{c}}
	case 20:
		return struct {
			*captureWriter
			pushCapability
			closeNotifyCapability
		}{c, pushCapability{c}, closeNotifyCapability{c}}
	case 21:
		return struct {
			*captureWriter
			flushCapability
			pushCapability
			closeNotifyCapability
		}{c, flushCapability{c}, pushCapability{c}, closeNotifyCapability{c}}
	case 22:
		return struct {
			*captureWriter
			hijackCapability
			pushCapability
			closeNotifyCapability
		}{c, hijackCapability{c}, pushCapability{c}, closeNotifyCapability{c}}
	case 23:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			pushCapability
			closeNotifyCapability
		}{c, flushCapability{c}, hijackCapability{c}, pushCapability{c}, closeNotifyCapability{c}}
	case 24:
		return struct {
			*captureWriter
			readFromCapability
			closeNotifyCapability
		}{c, readFromCapability{c}, closeNotifyCapability{c}}
	case 25:
		return struct {
			*captureWriter
			flushCapability
			readFromCapability
			closeNotifyCapability
		}{c, flushCapability{c}, readFromCapability{c}, closeNotifyCapability{c}}
	case 26:
		return struct {
			*captureWriter
			hijackCapability
			readFromCapability
			closeNotifyCapability
		}{c, hijackCapability{c}, readFromCapability{c}, closeNotifyCapability{c}}
	case 27:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			readFromCapability
			closeNotifyCapability
		}{c, flushCapability{c}, hijackCapability{c}, readFromCapability{c}, closeNotifyCapability{c}}
	case 28:
		return struct {
			*captureWriter
			pushCapability
			readFromCapability
			closeNotifyCapability
		}{c, pushCapability{c}, readFromCapability{c}, closeNotifyCapability{c}}
	case 29:
		return struct {
			*captureWriter
			flushCapability
			pushCapability
			readFromCapability
			closeNotifyCapability
		}{c, flushCapability{c}, pushCapability{c}, readFromCapability{c}, closeNotifyCapability{c}}
	case 30:
		return struct {
			*captureWriter
			hijackCapability
			pushCapability
			readFromCapability
			closeNotifyCapability
		}{c, hijackCapability{c}, pushCapability{c}, readFromCapability{c}, closeNotifyCapability{c}}
	case 31:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			pushCapability
			readFromCapability
			closeNotifyCapability
		}{c, flushCapability{c}, hijackCapability{c}, pushCapability{c}, readFromCapability{c}, closeNotifyCapability{c}}
	case 32:
		return struct {
			*captureWriter
			flushErrorCapability
		}{c, flushErrorCapability{c}}
	case 33:
		return struct {
			*captureWriter
			flushCapability
			flushErrorCapability
		}{c, flushCapability{c}, flushErrorCapability{c}}
	case 34:
		return struct {
			*captureWriter
			hijackCapability
			flushErrorCapability
		}{c, hijackCapability{c}, flushErrorCapability{c}}
	case 35:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			flushErrorCapability
		}{c, flushCapability{c}, hijackCapability{c}, flushErrorCapability{c}}
	case 36:
		return struct {
			*captureWriter
			pushCapability
			flushErrorCapability
		}{c, pushCapability{c}, flushErrorCapability{c}}
	case 37:
		return struct {
			*captureWriter
			flushCapability
			pushCapability
			flushErrorCapability
		}{c, flushCapability{c}, pushCapability{c}, flushErrorCapability{c}}
	case 38:
		return struct {
			*captureWriter
			hijackCapability
			pushCapability
			flushErrorCapability
		}{c, hijackCapability{c}, pushCapability{c}, flushErrorCapability{c}}
	case 39:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			pushCapability
			flushErrorCapability
		}{c, flushCapability{c}, hijackCapability{c}, pushCapability{c}, flushErrorCapability{c}}
	case 40:
		return struct {
			*captureWriter
			readFromCapability
			flushErrorCapability
		}{c, readFromCapability{c}, flushErrorCapability{c}}
	case 41:
		return struct {
			*captureWriter
			flushCapability
			readFromCapability
			flushErrorCapability
		}{c, flushCapability{c}, readFromCapability{c}, flushErrorCapability{c}}
	case 42:
		return struct {
			*captureWriter
			hijackCapability
			readFromCapability
			flushErrorCapability
		}{c, hijackCapability{c}, readFromCapability{c}, flushErrorCapability{c}}
	case 43:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			readFromCapability
			flushErrorCapability
		}{c, flushCapability{c}, hijackCapability{c}, readFromCapability{c}, flushErrorCapability{c}}
	case 44:
		return struct {
			*captureWriter
			pushCapability
			readFromCapability
			flushErrorCapability
		}{c, pushCapability{c}, readFromCapability{c}, flushErrorCapability{c}}
	case 45:
		return struct {
			*captureWriter
			flushCapability
			pushCapability
			readFromCapability
			flushErrorCapability
		}{c, flushCapability{c}, pushCapability{c}, readFromCapability{c}, flushErrorCapability{c}}
	case 46:
		return struct {
			*captureWriter
			hijackCapability
			pushCapability
			readFromCapability
			flushErrorCapability
		}{c, hijackCapability{c}, pushCapability{c}, readFromCapability{c}, flushErrorCapability{c}}
	case 47:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			pushCapability
			readFromCapability
			flushErrorCapability
		}{c, flushCapability{c}, hijackCapability{c}, pushCapability{c}, readFromCapability{c}, flushErrorCapability{c}}
	case 48:
		return struct {
			*captureWriter
			closeNotifyCapability
			flushErrorCapability
		}{c, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 49:
		return struct {
			*captureWriter
			flushCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, flushCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 50:
		return struct {
			*captureWriter
			hijackCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, hijackCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 51:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, flushCapability{c}, hijackCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 52:
		return struct {
			*captureWriter
			pushCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, pushCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 53:
		return struct {
			*captureWriter
			flushCapability
			pushCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, flushCapability{c}, pushCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 54:
		return struct {
			*captureWriter
			hijackCapability
			pushCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, hijackCapability{c}, pushCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 55:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			pushCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, flushCapability{c}, hijackCapability{c}, pushCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 56:
		return struct {
			*captureWriter
			readFromCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, readFromCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 57:
		return struct {
			*captureWriter
			flushCapability
			readFromCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, flushCapability{c}, readFromCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 58:
		return struct {
			*captureWriter
			hijackCapability
			readFromCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, hijackCapability{c}, readFromCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 59:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			readFromCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, flushCapability{c}, hijackCapability{c}, readFromCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 60:
		return struct {
			*captureWriter
			pushCapability
			readFromCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, pushCapability{c}, readFromCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 61:
		return struct {
			*captureWriter
			flushCapability
			pushCapability
			readFromCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, flushCapability{c}, pushCapability{c}, readFromCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 62:
		return struct {
			*captureWriter
			hijackCapability
			pushCapability
			readFromCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, hijackCapability{c}, pushCapability{c}, readFromCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	case 63:
		return struct {
			*captureWriter
			flushCapability
			hijackCapability
			pushCapability
			readFromCapability
			closeNotifyCapability
			flushErrorCapability
		}{c, flushCapability{c}, hijackCapability{c}, pushCapability{c}, readFromCapability{c}, closeNotifyCapability{c}, flushErrorCapability{c}}
	}
	panic("unreachable")
}
