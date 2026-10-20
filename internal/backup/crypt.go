package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
)

// 加密格式与 OpenSSL 兼容，没有面板也能解密：
//
//	openssl enc -d -aes-256-cbc -pbkdf2 -iter 100000 -md sha256 -in 备份.tar.gz.enc -out 备份.tar.gz
//
// 文件头为 "Salted__" + 8 字节盐，密钥和 IV 由 PBKDF2-HMAC-SHA256 派生，正文为 AES-256-CBC（PKCS#7 填充）。
const (
	pbkdf2Iter = 100000
	saltMagic  = "Salted__"
)

// DecryptCommand 是手动解密的命令，显示在界面和文档中。
const DecryptCommand = "openssl enc -d -aes-256-cbc -pbkdf2 -iter 100000 -md sha256 -in <备份文件> -out <备份文件>.tar.gz"

func deriveKey(password string, salt []byte) (key, iv []byte, err error) {
	k, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iter, 48)
	if err != nil {
		return nil, nil, err
	}
	return k[:32], k[32:], nil
}

type encWriter struct {
	w    io.Writer
	mode cipher.BlockMode
	buf  []byte
}

// NewEncrypter 返回加密写入器，Close 时写入最后一块（含填充），不会关闭 w。
func NewEncrypter(w io.Writer, password string) (io.WriteCloser, error) {
	salt := make([]byte, 8)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, iv, err := deriveKey(password, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(append([]byte(saltMagic), salt...)); err != nil {
		return nil, err
	}
	return &encWriter{w: w, mode: cipher.NewCBCEncrypter(block, iv)}, nil
}

func (e *encWriter) Write(p []byte) (int, error) {
	e.buf = append(e.buf, p...)
	n := len(e.buf) / aes.BlockSize * aes.BlockSize
	if n > 0 {
		out := make([]byte, n)
		e.mode.CryptBlocks(out, e.buf[:n])
		if _, err := e.w.Write(out); err != nil {
			return 0, err
		}
		e.buf = append(e.buf[:0], e.buf[n:]...)
	}
	return len(p), nil
}

func (e *encWriter) Close() error {
	pad := aes.BlockSize - len(e.buf)%aes.BlockSize
	e.buf = append(e.buf, bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(e.buf))
	e.mode.CryptBlocks(out, e.buf)
	_, err := e.w.Write(out)
	return err
}

// ErrBadPassword 表示备份密码错误（或文件损坏）。
var ErrBadPassword = errors.New("备份密码错误或文件已损坏")

type decReader struct {
	r    io.Reader
	mode cipher.BlockMode
	in   []byte // 还没解密的密文
	out  []byte // 已解密、可以返回的明文
	last []byte // 最后一块明文（到结尾时去掉填充）
	eof  bool
}

// NewDecrypter 返回解密读取器。
func NewDecrypter(r io.Reader, password string) (io.Reader, error) {
	hdr := make([]byte, 16)
	if _, err := io.ReadFull(r, hdr); err != nil || string(hdr[:8]) != saltMagic {
		return nil, errors.New("不是加密的备份文件")
	}
	key, iv, err := deriveKey(password, hdr[8:])
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return &decReader{r: r, mode: cipher.NewCBCDecrypter(block, iv)}, nil
}

func (d *decReader) Read(p []byte) (int, error) {
	for len(d.out) == 0 {
		if d.eof {
			return 0, io.EOF
		}
		buf := make([]byte, 64<<10)
		n, err := d.r.Read(buf)
		d.in = append(d.in, buf[:n]...)
		if errors.Is(err, io.EOF) {
			d.eof = true
		} else if err != nil {
			return 0, err
		}
		full := len(d.in) / aes.BlockSize * aes.BlockSize
		if d.eof && full != len(d.in) {
			return 0, ErrBadPassword
		}
		if full > 0 {
			plain := make([]byte, full)
			d.mode.CryptBlocks(plain, d.in[:full])
			d.in = append(d.in[:0], d.in[full:]...)
			plain = append(d.last, plain...)
			// 留住最后一块，等到结尾再去掉填充
			d.last = append([]byte(nil), plain[len(plain)-aes.BlockSize:]...)
			d.out = append(d.out, plain[:len(plain)-aes.BlockSize]...)
		}
		if d.eof {
			if len(d.last) != aes.BlockSize {
				return 0, ErrBadPassword
			}
			pad := int(d.last[aes.BlockSize-1])
			if pad < 1 || pad > aes.BlockSize || !bytes.Equal(d.last[aes.BlockSize-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
				return 0, ErrBadPassword
			}
			d.out = append(d.out, d.last[:aes.BlockSize-pad]...)
			d.last = nil
		}
	}
	n := copy(p, d.out)
	d.out = d.out[n:]
	return n, nil
}
