package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rc4"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
)

// padding is the 32-byte password padding string (ISO 32000-1 §7.6.3.3).
var padding = []byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56, 0xFF, 0xFA, 0x01, 0x08,
	0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80, 0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

type cryptMethod uint8

const (
	cmNone cryptMethod = iota
	cmRC4
	cmAESV2
	cmAESV3
)

// securityHandler implements the Standard security handler, revisions 2–6.
type securityHandler struct {
	key         []byte
	r           int
	strMethod   cryptMethod
	stmMethod   cryptMethod
	encryptMeta bool
	encryptRef  Ref
}

func newSecurityHandler(d Dict, id0 []byte, password string) (*securityHandler, error) {
	if f, _ := d["Filter"].(Name); f != "Standard" {
		return nil, fmt.Errorf("pdf: unsupported security handler %q", f)
	}
	v, _ := Int(d["V"])
	rev, _ := Int(d["R"])
	length := 40
	if l, ok := Int(d["Length"]); ok && l >= 40 {
		length = l
	}
	o := []byte(asString(d["O"]))
	u := []byte(asString(d["U"]))
	p, _ := Int(d["P"])
	h := &securityHandler{r: rev, encryptMeta: true}
	if b, ok := d["EncryptMetadata"].(Boolean); ok {
		h.encryptMeta = bool(b)
	}
	h.strMethod, h.stmMethod = cmRC4, cmRC4
	if v >= 4 {
		cf, _ := d["CF"].(Dict)
		method := func(name Name) cryptMethod {
			if name == "Identity" || name == "" {
				return cmNone
			}
			fd, _ := cf[name].(Dict)
			switch fd["CFM"] {
			case Name("AESV2"):
				return cmAESV2
			case Name("AESV3"):
				return cmAESV3
			case Name("None"):
				return cmNone
			}
			return cmRC4
		}
		stmf, _ := d["StmF"].(Name)
		strf, _ := d["StrF"].(Name)
		h.stmMethod, h.strMethod = method(stmf), method(strf)
		if fd, ok := cf[stmf].(Dict); ok {
			if l, ok := Int(fd["Length"]); ok {
				if l <= 32 { // some writers give bytes, not bits
					l *= 8
				}
				length = l
			}
		}
	}
	if rev >= 5 {
		oe := []byte(asString(d["OE"]))
		ue := []byte(asString(d["UE"]))
		key, err := r6Key([]byte(password), o, u, oe, ue, rev)
		if err != nil {
			return nil, err
		}
		h.key = key
		return h, nil
	}
	n := length / 8
	if rev == 2 {
		n = 5
	}
	if n < 5 || n > 16 {
		n = 16
	}
	pw := []byte(password)
	if key := r4Key(pw, o, p, id0, rev, n, h.encryptMeta); checkUser(key, u, id0, rev) {
		h.key = key
		return h, nil
	}
	// Try as owner password: recover the user password from /O.
	userPw := ownerToUser(pw, o, rev, n)
	if key := r4Key(userPw, o, p, id0, rev, n, h.encryptMeta); checkUser(key, u, id0, rev) {
		h.key = key
		return h, nil
	}
	return nil, ErrEncrypted
}

func asString(o Object) string {
	s, _ := o.(String)
	return string(s)
}

func padPassword(pw []byte) []byte {
	out := make([]byte, 32)
	n := copy(out, pw)
	copy(out[n:], padding)
	return out
}

// r4Key implements Algorithm 2.
func r4Key(pw, o []byte, p int, id0 []byte, rev, n int, encMeta bool) []byte {
	m := md5.New()
	m.Write(padPassword(pw))
	m.Write(o[:min(len(o), 32)])
	var pb [4]byte
	binary.LittleEndian.PutUint32(pb[:], uint32(int32(p)))
	m.Write(pb[:])
	m.Write(id0)
	if rev >= 4 && !encMeta {
		m.Write([]byte{0xff, 0xff, 0xff, 0xff})
	}
	key := m.Sum(nil)
	if rev >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(key[:n])
			key = s[:]
		}
	}
	return key[:n]
}

// checkUser implements Algorithms 4/5 (user password verification).
func checkUser(key, u, id0 []byte, rev int) bool {
	if len(u) < 16 {
		return false
	}
	if rev == 2 {
		c, _ := rc4.NewCipher(key)
		out := make([]byte, 32)
		c.XORKeyStream(out, padding)
		return bytes.Equal(out[:16], u[:16]) // tolerate trailing bytes
	}
	m := md5.New()
	m.Write(padding)
	m.Write(id0)
	buf := m.Sum(nil)
	tmp := make([]byte, len(key))
	for i := 0; i < 20; i++ {
		for j := range key {
			tmp[j] = key[j] ^ byte(i)
		}
		c, _ := rc4.NewCipher(tmp)
		c.XORKeyStream(buf, buf)
	}
	return bytes.Equal(buf[:16], u[:16])
}

// ownerToUser implements Algorithm 7 (recovering the user password).
func ownerToUser(ownerPw, o []byte, rev, n int) []byte {
	s := md5.Sum(padPassword(ownerPw))
	key := s[:]
	if rev >= 3 {
		for i := 0; i < 50; i++ {
			s = md5.Sum(key)
			key = s[:]
		}
	}
	key = key[:n]
	out := append([]byte(nil), o[:min(len(o), 32)]...)
	if rev == 2 {
		c, _ := rc4.NewCipher(key)
		c.XORKeyStream(out, out)
		return out
	}
	tmp := make([]byte, n)
	for i := 19; i >= 0; i-- {
		for j := range key {
			tmp[j] = key[j] ^ byte(i)
		}
		c, _ := rc4.NewCipher(tmp)
		c.XORKeyStream(out, out)
	}
	return out
}

// r6Key implements Algorithms 2.A / 2.B (AES-256, revisions 5 and 6).
func r6Key(pw, o, u, oe, ue []byte, rev int) ([]byte, error) {
	if len(pw) > 127 {
		pw = pw[:127]
	}
	if len(u) < 48 || len(o) < 48 {
		return nil, errors.New("pdf: invalid /U or /O for AES-256")
	}
	hashFn := func(pw, salt, udata []byte) []byte {
		if rev == 5 {
			h := sha256.New()
			h.Write(pw)
			h.Write(salt)
			h.Write(udata)
			return h.Sum(nil)
		}
		return hash2B(pw, salt, udata)
	}
	decryptKey := func(k, enc []byte) ([]byte, error) {
		if len(enc) < 32 {
			return nil, errors.New("pdf: invalid /OE or /UE")
		}
		blk, err := aes.NewCipher(k)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 32)
		cipher.NewCBCDecrypter(blk, make([]byte, 16)).CryptBlocks(out, enc[:32])
		return out, nil
	}
	// Owner password first, then user password.
	if bytes.Equal(hashFn(pw, o[32:40], u[:48]), o[:32]) {
		return decryptKey(hashFn(pw, o[40:48], u[:48]), oe)
	}
	if bytes.Equal(hashFn(pw, u[32:40], nil), u[:32]) {
		return decryptKey(hashFn(pw, u[40:48], nil), ue)
	}
	return nil, ErrEncrypted
}

func hash2B(pw, salt, udata []byte) []byte {
	h := sha256.New()
	h.Write(pw)
	h.Write(salt)
	h.Write(udata)
	k := h.Sum(nil)
	var hs [3]hash.Hash = [3]hash.Hash{sha256.New(), sha512.New384(), sha512.New()}
	for i := 0; ; i++ {
		k1 := make([]byte, 0, 64*(len(pw)+len(k)+len(udata)))
		for j := 0; j < 64; j++ {
			k1 = append(k1, pw...)
			k1 = append(k1, k...)
			k1 = append(k1, udata...)
		}
		blk, _ := aes.NewCipher(k[:16])
		e := make([]byte, len(k1))
		cipher.NewCBCEncrypter(blk, k[16:32]).CryptBlocks(e, k1)
		sum := 0
		for _, b := range e[:16] {
			sum += int(b)
		}
		hh := hs[sum%3]
		hh.Reset()
		hh.Write(e)
		k = hh.Sum(nil)
		if i >= 63 && int(e[len(e)-1]) <= i-31 { // "round number" is 1-based
			break
		}
	}
	return k[:32]
}

func (h *securityHandler) objectKey(ref Ref, m cryptMethod) []byte {
	if m == cmAESV3 {
		return h.key
	}
	b := make([]byte, 0, len(h.key)+9)
	b = append(b, h.key...)
	b = append(b, byte(ref.Num), byte(ref.Num>>8), byte(ref.Num>>16), byte(ref.Gen), byte(ref.Gen>>8))
	if m == cmAESV2 {
		b = append(b, "sAlT"...)
	}
	s := md5.Sum(b)
	return s[:min(len(h.key)+5, 16)]
}

func (h *securityHandler) decrypt(ref Ref, m cryptMethod, data []byte) []byte {
	switch m {
	case cmNone:
		return data
	case cmRC4:
		c, _ := rc4.NewCipher(h.objectKey(ref, m))
		out := make([]byte, len(data))
		c.XORKeyStream(out, data)
		return out
	}
	// AES-CBC, IV prefix, PKCS#5 padding.
	if len(data) < 32 || len(data)%16 != 0 {
		if len(data) >= 16 && len(data)%16 != 0 {
			data = data[:len(data)-len(data)%16]
		}
		if len(data) < 32 {
			return nil
		}
	}
	blk, err := aes.NewCipher(h.objectKey(ref, m))
	if err != nil {
		return data
	}
	out := make([]byte, len(data)-16)
	cipher.NewCBCDecrypter(blk, data[:16]).CryptBlocks(out, data[16:])
	if n := int(out[len(out)-1]); n >= 1 && n <= 16 && n <= len(out) {
		out = out[:len(out)-n]
	}
	return out
}

func (h *securityHandler) stringDecrypter(ref Ref) func(String) String {
	return func(s String) String { return String(h.decrypt(ref, h.strMethod, []byte(s))) }
}

func (h *securityHandler) decryptStream(ref Ref, s *Stream) []byte {
	switch s.Dict["Type"] {
	case Name("XRef"):
		return s.Raw
	case Name("Metadata"):
		if !h.encryptMeta {
			return s.Raw
		}
	}
	// A stream-level /Crypt filter with /Identity disables encryption.
	if fa, ok := s.Dict["Filter"].(Array); ok && len(fa) > 0 && fa[0] == Name("Crypt") {
		if dp, ok := s.Dict["DecodeParms"].(Array); ok && len(dp) > 0 {
			if d, ok := dp[0].(Dict); ok && (d["Name"] == nil || d["Name"] == Name("Identity")) {
				return s.Raw
			}
		}
	}
	return h.decrypt(ref, h.stmMethod, s.Raw)
}
