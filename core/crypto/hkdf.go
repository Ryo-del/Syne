package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"hash"
	"io"
)

// Minimal HKDF-SHA256 reader (extract+expand), to avoid extra deps and keep Go stdlib only.
// This is sufficient for deriving fixed-length keys from ECDH secrets.
func hkdfSHA256(ikm, salt, info []byte) io.Reader {
	prk := hkdfExtract(sha256.New, salt, ikm)
	return hkdfExpand(sha256.New, prk, info)
}

func hkdfExtract(h func() hash.Hash, salt, ikm []byte) []byte {
	if salt == nil {
		salt = make([]byte, h().Size())
	}
	mac := hmac.New(h, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

func hkdfExpand(h func() hash.Hash, prk, info []byte) io.Reader {
	return &hkdfReader{h: h, prk: prk, info: info}
}

type hkdfReader struct {
	h    func() hash.Hash
	prk  []byte
	info []byte

	counter byte
	prev    []byte
	buf     []byte
}

func (r *hkdfReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(r.buf) == 0 {
			if r.counter == 255 {
				return n, io.EOF
			}
			r.counter++
			mac := hmac.New(r.h, r.prk)
			mac.Write(r.prev)
			mac.Write(r.info)
			mac.Write([]byte{r.counter})
			r.prev = mac.Sum(nil)
			r.buf = r.prev
		}

		c := copy(p[n:], r.buf)
		r.buf = r.buf[c:]
		n += c
	}
	return n, nil
}

