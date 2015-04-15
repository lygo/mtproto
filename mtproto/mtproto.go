package mtproto

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"reflect"
)

const (
	// системные конструкторы
	bool_false           = 0xbc799737
	bool_true            = 0x997275b5
	vector               = 0x1cb5c415
	msg_container        = 0x73f1f8dc
	new_session_created  = 0x9ec20908
	msgs_ack             = 0x62d6b459
	rpc_result           = 0xf35c6d01
	rpc_error            = 0x2144ca19
	bad_msg_notification = 0xa7eff811
	bad_server_salt      = 0xedab447b

	// конструкторы авторизации
	req_pq                = 0x60469778
	resPQ                 = 0x05162463
	p_q_inner_data        = 0x83c95aec
	req_DH_params         = 0xd712e4be
	server_DH_params_ok   = 0xd0e8075c
	server_DH_params_fail = 0x79cb045d
	server_DH_inner_data  = 0xb5890dba
	client_DH_inner_data  = 0x6643b654
	set_client_DH_params  = 0xf5045f1f
	dh_gen_ok             = 0x3bcbf734
	dh_gen_retry          = 0x46dc1fb9
	dh_gen_fail           = 0xa69dae02
)

type MTProto struct {
	g_ab       *big.Int
	serverSalt uint64
	conn       *net.TCPConn
	encrypted  bool

	buf  []byte
	size int
	off  int

	level     int
	messageId uint64
	seqNo     int32

	data interface{}
}

type TL_resPQ struct {
	nonce        []byte
	server_nonce []byte
	pq           *big.Int
	fingerprints []uint64
}

func (m *MTProto) Connect(addr string) error {
	var err error
	var tcpAddr *net.TCPAddr

	m.g_ab = new(big.Int)
	m.serverSalt = 0
	m.encrypted = false

	tcpAddr, err = net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return err
	}

	m.conn, err = net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		return err
	}

	_, err = m.conn.Write([]byte{0xef})
	if err != nil {
		return err
	}

	return nil
}

func (m *MTProto) SendPacket(msg []byte) error {
	var x []byte

	if m.encrypted {
		// TODO: encrypt packet

	} else {
		x = append(x, EncodeLong(0)...)
		x = append(x, EncodeLong(GenerateMessageId())...)
		x = append(x, EncodeInt(int32(len(msg)))...)
		x = append(x, msg...)

	}

	len := len(x) / 4
	if len < 127 {
		x = append([]byte{byte(len)}, x...)
	} else {
		x = append(EncodeInt(int32(len<<8|127)), x...)
	}

	_, err := m.conn.Write(x)
	if err != nil {
		return err
	}

	return nil
}

func (m *MTProto) Handshake() error {
	var x []byte
	var err error

	// (send) req_pq
	nonce := GenerateNonce(16)
	x = append(x, EncodeUInt(req_pq)...)
	x = append(x, nonce...)
	err = m.SendPacket(x)
	if err != nil {
		return err
	}

	// (parse) resPQ
	err = m.Read()
	if err != nil {
		return err
	}
	res, ok := m.data.(TL_resPQ)
	if !ok {
		return errors.New("Handshake: ожидался TL_resPQ")
	}
	if !bytes.Equal(nonce, res.nonce) {
		return errors.New("Handshake: не совпадает nonce")
	}
	found := false
	for _, b := range res.fingerprints {
		if b == 14101943622620965665 {
			found = true
			break
		}
	}
	if !found {
		return errors.New("Handshake: нет отпечатка нужного ключа")
	}

	// (encoding) p_q_inner_data
	// (send) req_DH_params
	// (parse) server_DH_params_{ok, fail}
	// (send) set_client_DH_params
	// (parse) dh_gen_{ok, retry, fail}

	return nil
}

func (m *MTProto) Read() error {
	var err error
	var n int

	b := make([]byte, 1)
	n, err = m.conn.Read(b)
	if err != nil {
		return err
	}

	if b[0] < 127 {
		m.size = int(b[0]) << 2
	} else {
		b := make([]byte, 3)
		n, err = m.conn.Read(b)
		if err != nil {
			return err
		}
		m.size = int(b[0]) | int(b[1])<<8 | int(b[2])<<16
	}

	left := m.size
	m.buf = make([]byte, m.size)
	for left > 0 {
		n, err = m.conn.Read(m.buf[m.size-left:])
		if err != nil {
			return err
		}
		left -= n
	}
	m.off = 0

	if m.size == 4 {
		return fmt.Errorf("Ошибка: %s", hex.EncodeToString(m.buf))
	}

	if m.size <= 8 {
		return fmt.Errorf("Слишком маленький пакет: %d байт", m.size)
	}

	fmt.Print(hex.Dump(m.buf))

	authKeyHash, err := m.DecodeLong()
	if authKeyHash == 0 {
		m.messageId, err = m.DecodeLong()
		if err != nil {
			return err
		}
		messageLen, err := m.DecodeInt()
		if err != nil {
			return err
		}
		if int(messageLen) != m.size-20 {
			return fmt.Errorf("Заявленная длина сообщения не совпадает: %d (должна быть %d)", messageLen, m.size-20)
		}
		mod := m.messageId & 3
		if mod != 1 && mod != 3 {
			return fmt.Errorf("Невалидные битые message_id: %d", mod)
		}
		m.seqNo = 0
		m.level = 0

		err = m.DecodePacket()
		if err != nil {
			return err
		}

	} else {
		// TODO: read encrypted packet

	}

	return nil
}

func (m *MTProto) Dump() {
	fmt.Printf("AB: %v\tSALT: %v\tConnection: %v\n", m.g_ab, m.serverSalt, m.conn)
	fmt.Println(reflect.TypeOf(m.data), m.data)
}

func (m *MTProto) DecodePacket() error {
	var err error

	constructor, err := m.DecodeUInt()
	if err != nil {
		return err
	}

	m.level++

	switch constructor {
	case resPQ:
		nonce, err := m.DecodeBytes(16)
		server_nonce, err := m.DecodeBytes(16)
		pq, err := m.DecodeBigInt()
		fingerprints, err := m.DecodeVectorLong()
		m.data = TL_resPQ{nonce, server_nonce, pq, fingerprints}
		if err != nil {
			return err
		}

	default:
		return fmt.Errorf("Неизвестный конструктор: %08x", constructor)
	}

	m.level--

	return nil
}

func (m *MTProto) DecodeLong() (r uint64, err error) {
	if m.off+8 > m.size {
		return 0, errors.New("DecodeLong: короткий пакет")
	}
	x := binary.LittleEndian.Uint64(m.buf[m.off : m.off+8])
	m.off += 8
	return x, nil
}

func (m *MTProto) DecodeInt() (r int32, err error) {
	if m.off+4 > m.size {
		return 0, errors.New("DecodeInt: короткий пакет")
	}
	x := binary.LittleEndian.Uint32(m.buf[m.off : m.off+4])
	m.off += 4
	return int32(x), nil
}

func (m *MTProto) DecodeUInt() (r uint32, err error) {
	if m.off+4 > m.size {
		return 0, errors.New("DecodeUInt: короткий пакет")
	}
	x := binary.LittleEndian.Uint32(m.buf[m.off : m.off+4])
	m.off += 4
	return x, nil
}

func (m *MTProto) DecodeBytes(size int) (r []byte, err error) {
	if m.off+size > m.size {
		return nil, errors.New("DecodeBytes: короткий пакет")
	}
	x := make([]byte, size)
	copy(x, m.buf[m.off:m.off+size])
	m.off += size
	return x, nil
}

func (m *MTProto) DecodeStringBytes() (r []byte, err error) {
	var size, padding int

	if m.off+1 > m.size {
		return nil, errors.New("DecodeStringBytes: короткий пакет")
	}
	size = int(m.buf[m.off])
	m.off++
	padding = (4 - ((size + 1) % 4)) & 3
	if size == 254 {
		if m.off+3 > m.size {
			return nil, errors.New("DecodeStringBytes: короткий пакет")
		}
		size = int(m.buf[m.off]) | int(m.buf[m.off+1])<<8 | int(m.buf[m.off+2])<<16
		m.off += 3
		padding = (4 - size%4) & 3
	}

	if m.off+size > m.size {
		return nil, errors.New("DecodeStringBytes: короткий пакет (size)")
	}
	x := make([]byte, size)
	copy(x, m.buf[m.off:m.off+size])
	m.off += size

	if m.off+padding > m.size {
		return nil, errors.New("DecodeStringBytes: короткий пакет (padding)")
	}
	m.off += padding

	return x, nil
}

func (m *MTProto) DecodeBigInt() (r *big.Int, err error) {
	b, err := m.DecodeStringBytes()
	if err != nil {
		return nil, err
	}
	y := make([]byte, len(b)+1)
	y[0] = 0
	copy(y[1:], b)
	x := new(big.Int).SetBytes(y)
	return x, nil
}

func (m *MTProto) DecodeVectorLong() (r []uint64, err error) {
	constructor, err := m.DecodeUInt()
	if err != nil {
		return nil, err
	}
	if constructor != vector {
		return nil, errors.New("DecodeVectorLong: Неправильный конструктор")
	}
	size, err := m.DecodeInt()
	if err != nil {
		return nil, err
	}
	if size <= 0 {
		return nil, errors.New("DecodeVectorLong: Неправильный размер")
	}
	x := make([]uint64, size)
	i := int32(0)
	for i < size {
		y, err := m.DecodeLong()
		if err != nil {
			return nil, err
		}
		x[i] = y
		i++
	}
	return x, nil
}