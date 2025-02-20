// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package quic

import (
	"bytes"
	"crypto/tls"
	"reflect"
	"testing"
	"time"
)

func TestParseLongHeaderPacket(t *testing.T) {
	// Example Initial packet from:
	// https://www.rfc-editor.org/rfc/rfc9001.html#section-a.3
	cid := unhex(`8394c8f03e515708`)
	_, initialServerKeys := initialKeys(cid)
	pkt := unhex(`
		cf000000010008f067a5502a4262b500 4075c0d95a482cd0991cd25b0aac406a
		5816b6394100f37a1c69797554780bb3 8cc5a99f5ede4cf73c3ec2493a1839b3
		dbcba3f6ea46c5b7684df3548e7ddeb9 c3bf9c73cc3f3bded74b562bfb19fb84
		022f8ef4cdd93795d77d06edbb7aaf2f 58891850abbdca3d20398c276456cbc4
		2158407dd074ee
	`)
	want := longPacket{
		ptype:     packetTypeInitial,
		version:   1,
		num:       1,
		dstConnID: []byte{},
		srcConnID: unhex(`f067a5502a4262b5`),
		payload: unhex(`
			02000000000600405a020000560303ee fce7f7b37ba1d1632e96677825ddf739
			88cfc79825df566dc5430b9a045a1200 130100002e00330024001d00209d3c94
			0d89690b84d08a60993c144eca684d10 81287c834d5311bcf32bb9da1a002b00
			020304
		`),
		extra: []byte{},
	}

	// Parse the packet.
	got, n := parseLongHeaderPacket(pkt, initialServerKeys, 0)
	if n != len(pkt) {
		t.Errorf("parseLongHeaderPacket: n=%v, want %v", n, len(pkt))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseLongHeaderPacket:\n got: %+v\nwant: %+v", got, want)
	}

	// Skip the packet.
	if got, want := skipLongHeaderPacket(pkt), len(pkt); got != want {
		t.Errorf("skipLongHeaderPacket: n=%v, want %v", got, want)
	}

	// Parse truncated versions of the packet; every attempt should fail.
	for i := 0; i < len(pkt); i++ {
		if _, n := parseLongHeaderPacket(pkt[:i], initialServerKeys, 0); n != -1 {
			t.Fatalf("parse truncated long header packet: n=%v, want -1\ninput: %x", n, pkt[:i])
		}
		if n := skipLongHeaderPacket(pkt[:i]); n != -1 {
			t.Errorf("skip truncated long header packet: n=%v, want -1", n)
		}
	}

	// Parse with the wrong keys.
	_, invalidKeys := initialKeys([]byte{})
	if _, n := parseLongHeaderPacket(pkt, invalidKeys, 0); n != -1 {
		t.Fatalf("parse long header packet with wrong keys: n=%v, want -1", n)
	}
}

func TestRoundtripEncodeLongPacket(t *testing.T) {
	aes128Keys, _ := newKeys(tls.TLS_AES_128_GCM_SHA256, []byte("secret"))
	aes256Keys, _ := newKeys(tls.TLS_AES_256_GCM_SHA384, []byte("secret"))
	chachaKeys, _ := newKeys(tls.TLS_CHACHA20_POLY1305_SHA256, []byte("secret"))
	for _, test := range []struct {
		desc string
		p    longPacket
		k    keys
	}{{
		desc: "Initial, 1-byte number, AES128",
		p: longPacket{
			ptype:     packetTypeInitial,
			version:   0x11223344,
			num:       0, // 1-byte encodeing
			dstConnID: []byte{1, 2, 3, 4},
			srcConnID: []byte{5, 6, 7, 8},
			payload:   []byte("payload"),
			extra:     []byte("token"),
		},
		k: aes128Keys,
	}, {
		desc: "0-RTT, 2-byte number, AES256",
		p: longPacket{
			ptype:     packetType0RTT,
			version:   0x11223344,
			num:       0x100, // 2-byte encoding
			dstConnID: []byte{1, 2, 3, 4},
			srcConnID: []byte{5, 6, 7, 8},
			payload:   []byte("payload"),
		},
		k: aes256Keys,
	}, {
		desc: "0-RTT, 3-byte number, AES256",
		p: longPacket{
			ptype:     packetType0RTT,
			version:   0x11223344,
			num:       0x10000, // 2-byte encoding
			dstConnID: []byte{1, 2, 3, 4},
			srcConnID: []byte{5, 6, 7, 8},
			payload:   []byte{0},
		},
		k: aes256Keys,
	}, {
		desc: "Handshake, 4-byte number, ChaCha20Poly1305",
		p: longPacket{
			ptype:     packetTypeHandshake,
			version:   0x11223344,
			num:       0x1000000, // 4-byte encoding
			dstConnID: []byte{1, 2, 3, 4},
			srcConnID: []byte{5, 6, 7, 8},
			payload:   []byte("payload"),
		},
		k: chachaKeys,
	}} {
		t.Run(test.desc, func(t *testing.T) {
			var w packetWriter
			w.reset(1200)
			w.startProtectedLongHeaderPacket(0, test.p)
			w.b = append(w.b, test.p.payload...)
			w.finishProtectedLongHeaderPacket(0, test.k, test.p)
			pkt := w.datagram()

			got, n := parseLongHeaderPacket(pkt, test.k, 0)
			if n != len(pkt) {
				t.Errorf("parseLongHeaderPacket: n=%v, want %v", n, len(pkt))
			}
			if !reflect.DeepEqual(got, test.p) {
				t.Errorf("Round-trip encode/decode did not preserve packet.\nsent: %+v\n got: %+v\nwire: %x", test.p, got, pkt)
			}
		})
	}
}

func TestRoundtripEncodeShortPacket(t *testing.T) {
	aes128Keys, _ := newKeys(tls.TLS_AES_128_GCM_SHA256, []byte("secret"))
	aes256Keys, _ := newKeys(tls.TLS_AES_256_GCM_SHA384, []byte("secret"))
	chachaKeys, _ := newKeys(tls.TLS_CHACHA20_POLY1305_SHA256, []byte("secret"))
	connID := make([]byte, connIDLen)
	for i := range connID {
		connID[i] = byte(i)
	}
	for _, test := range []struct {
		desc    string
		num     packetNumber
		payload []byte
		k       keys
	}{{
		desc:    "1-byte number, AES128",
		num:     0, // 1-byte encoding,
		payload: []byte("payload"),
		k:       aes128Keys,
	}, {
		desc:    "2-byte number, AES256",
		num:     0x100, // 2-byte encoding
		payload: []byte("payload"),
		k:       aes256Keys,
	}, {
		desc:    "3-byte number, ChaCha20Poly1305",
		num:     0x10000, // 3-byte encoding
		payload: []byte("payload"),
		k:       chachaKeys,
	}, {
		desc:    "4-byte number, ChaCha20Poly1305",
		num:     0x1000000, // 4-byte encoding
		payload: []byte{0},
		k:       chachaKeys,
	}} {
		t.Run(test.desc, func(t *testing.T) {
			var w packetWriter
			w.reset(1200)
			w.start1RTTPacket(test.num, 0, connID)
			w.b = append(w.b, test.payload...)
			w.finish1RTTPacket(test.num, 0, connID, test.k)
			pkt := w.datagram()
			p, n := parse1RTTPacket(pkt, test.k, 0)
			if n != len(pkt) {
				t.Errorf("parse1RTTPacket: n=%v, want %v", n, len(pkt))
			}
			if p.num != test.num || !bytes.Equal(p.payload, test.payload) {
				t.Errorf("Round-trip encode/decode did not preserve packet.\nsent: num=%v, payload={%x}\ngot: num=%v, payload={%x}", test.num, test.payload, p.num, p.payload)
			}
		})
	}
}

func TestFrameEncodeDecode(t *testing.T) {
	for _, test := range []struct {
		s         string
		f         debugFrame
		b         []byte
		truncated []byte
	}{{
		s: "PADDING*1",
		f: debugFramePadding{
			size: 1,
		},
		b: []byte{
			0x00, // Type (i) = 0x00,

		},
	}, {
		s: "PING",
		f: debugFramePing{},
		b: []byte{
			0x01, // TYPE(i) = 0x01
		},
	}, {
		s: "ACK Delay=80µs [0,16) [17,32) [48,64)",
		f: debugFrameAck{
			ackDelay: (10 << ackDelayExponent) * time.Microsecond,
			ranges: []i64range{
				{0x00, 0x10},
				{0x11, 0x20},
				{0x30, 0x40},
			},
		},
		b: []byte{
			0x02, // TYPE (i) = 0x02
			0x3f, // Largest Acknowledged (i)
			10,   // ACK Delay (i)
			0x02, // ACK Range Count (i)
			0x0f, // First ACK Range (i)
			0x0f, // Gap (i)
			0x0e, // ACK Range Length (i)
			0x00, // Gap (i)
			0x0f, // ACK Range Length (i)
		},
		truncated: []byte{
			0x02, // TYPE (i) = 0x02
			0x3f, // Largest Acknowledged (i)
			10,   // ACK Delay (i)
			0x01, // ACK Range Count (i)
			0x0f, // First ACK Range (i)
			0x0f, // Gap (i)
			0x0e, // ACK Range Length (i)
		},
	}, {
		s: "RESET_STREAM ID=1 Code=2 FinalSize=3",
		f: debugFrameResetStream{
			id:        1,
			code:      2,
			finalSize: 3,
		},
		b: []byte{
			0x04, // TYPE(i) = 0x04
			0x01, // Stream ID (i),
			0x02, // Application Protocol Error Code (i),
			0x03, // Final Size (i),
		},
	}, {
		s: "STOP_SENDING ID=1 Code=2",
		f: debugFrameStopSending{
			id:   1,
			code: 2,
		},
		b: []byte{
			0x05, // TYPE(i) = 0x05
			0x01, // Stream ID (i),
			0x02, // Application Protocol Error Code (i),
		},
	}, {
		s: "CRYPTO Offset=1 Length=2",
		f: debugFrameCrypto{
			off:  1,
			data: []byte{3, 4},
		},
		b: []byte{
			0x06,       // Type (i) = 0x06,
			0x01,       // Offset (i),
			0x02,       // Length (i),
			0x03, 0x04, // Crypto Data (..),
		},
		truncated: []byte{
			0x06, // Type (i) = 0x06,
			0x01, // Offset (i),
			0x01, // Length (i),
			0x03,
		},
	}, {
		s: "NEW_TOKEN Token=0304",
		f: debugFrameNewToken{
			token: []byte{3, 4},
		},
		b: []byte{
			0x07,       // Type (i) = 0x07,
			0x02,       // Token Length (i),
			0x03, 0x04, // Token (..),
		},
	}, {
		s: "STREAM ID=1 Offset=0 Length=0",
		f: debugFrameStream{
			id:   1,
			fin:  false,
			off:  0,
			data: []byte{},
		},
		b: []byte{
			0x0a, // Type (i) = 0x08..0x0f,
			0x01, // Stream ID (i),
			// [Offset (i)],
			0x00, // [Length (i)],
			// Stream Data (..),
		},
	}, {
		s: "STREAM ID=100 Offset=4 Length=3",
		f: debugFrameStream{
			id:   100,
			fin:  false,
			off:  4,
			data: []byte{0xa0, 0xa1, 0xa2},
		},
		b: []byte{
			0x0e,       // Type (i) = 0x08..0x0f,
			0x40, 0x64, // Stream ID (i),
			0x04,             // [Offset (i)],
			0x03,             // [Length (i)],
			0xa0, 0xa1, 0xa2, // Stream Data (..),
		},
		truncated: []byte{
			0x0e,       // Type (i) = 0x08..0x0f,
			0x40, 0x64, // Stream ID (i),
			0x04,       // [Offset (i)],
			0x02,       // [Length (i)],
			0xa0, 0xa1, // Stream Data (..),
		},
	}, {
		s: "STREAM ID=100 FIN Offset=4 Length=3",
		f: debugFrameStream{
			id:   100,
			fin:  true,
			off:  4,
			data: []byte{0xa0, 0xa1, 0xa2},
		},
		b: []byte{
			0x0f,       // Type (i) = 0x08..0x0f,
			0x40, 0x64, // Stream ID (i),
			0x04,             // [Offset (i)],
			0x03,             // [Length (i)],
			0xa0, 0xa1, 0xa2, // Stream Data (..),
		},
		truncated: []byte{
			0x0e,       // Type (i) = 0x08..0x0f,
			0x40, 0x64, // Stream ID (i),
			0x04,       // [Offset (i)],
			0x02,       // [Length (i)],
			0xa0, 0xa1, // Stream Data (..),
		},
	}, {
		s: "STREAM ID=1 FIN Offset=100 Length=0",
		f: debugFrameStream{
			id:   1,
			fin:  true,
			off:  100,
			data: []byte{},
		},
		b: []byte{
			0x0f,       // Type (i) = 0x08..0x0f,
			0x01,       // Stream ID (i),
			0x40, 0x64, // [Offset (i)],
			0x00, // [Length (i)],
			// Stream Data (..),
		},
	}, {
		s: "MAX_DATA Max=10",
		f: debugFrameMaxData{
			max: 10,
		},
		b: []byte{
			0x10, // Type (i) = 0x10,
			0x0a, // Maximum Data (i),
		},
	}, {
		s: "MAX_STREAM_DATA ID=1 Max=10",
		f: debugFrameMaxStreamData{
			id:  1,
			max: 10,
		},
		b: []byte{
			0x11, // Type (i) = 0x11,
			0x01, // Stream ID (i),
			0x0a, // Maximum Stream Data (i),
		},
	}, {
		s: "MAX_STREAMS Type=bidi Max=1",
		f: debugFrameMaxStreams{
			streamType: bidiStream,
			max:        1,
		},
		b: []byte{
			0x12, //   Type (i) = 0x12..0x13,
			0x01, // Maximum Streams (i),
		},
	}, {
		s: "MAX_STREAMS Type=uni Max=1",
		f: debugFrameMaxStreams{
			streamType: uniStream,
			max:        1,
		},
		b: []byte{
			0x13, //   Type (i) = 0x12..0x13,
			0x01, // Maximum Streams (i),
		},
	}, {
		s: "DATA_BLOCKED Max=1",
		f: debugFrameDataBlocked{
			max: 1,
		},
		b: []byte{
			0x14, // Type (i) = 0x14,
			0x01, // Maximum Data (i),
		},
	}, {
		s: "STREAM_DATA_BLOCKED ID=1 Max=2",
		f: debugFrameStreamDataBlocked{
			id:  1,
			max: 2,
		},
		b: []byte{
			0x15, // Type (i) = 0x15,
			0x01, // Stream ID (i),
			0x02, // Maximum Stream Data (i),
		},
	}, {
		s: "STREAMS_BLOCKED Type=bidi Max=1",
		f: debugFrameStreamsBlocked{
			streamType: bidiStream,
			max:        1,
		},
		b: []byte{
			0x16, // Type (i) = 0x16..0x17,
			0x01, // Maximum Streams (i),
		},
	}, {
		s: "STREAMS_BLOCKED Type=uni Max=1",
		f: debugFrameStreamsBlocked{
			streamType: uniStream,
			max:        1,
		},
		b: []byte{
			0x17, // Type (i) = 0x16..0x17,
			0x01, // Maximum Streams (i),
		},
	}, {
		s: "NEW_CONNECTION_ID Seq=3 Retire=2 ID=a0a1a2a3 Token=0102030405060708090a0b0c0d0e0f10",
		f: debugFrameNewConnectionID{
			seq:           3,
			retirePriorTo: 2,
			connID:        []byte{0xa0, 0xa1, 0xa2, 0xa3},
			token:         [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		},
		b: []byte{
			0x18,                   // Type (i) = 0x18,
			0x03,                   // Sequence Number (i),
			0x02,                   // Retire Prior To (i),
			0x04,                   // Length (8),
			0xa0, 0xa1, 0xa2, 0xa3, // Connection ID (8..160),
			1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, // Stateless Reset Token (128),
		},
	}, {
		s: "RETIRE_CONNECTION_ID Seq=1",
		f: debugFrameRetireConnectionID{
			seq: 1,
		},
		b: []byte{
			0x19, // Type (i) = 0x19,
			0x01, // Sequence Number (i),
		},
	}, {
		s: "PATH_CHALLENGE Data=0123456789abcdef",
		f: debugFramePathChallenge{
			data: 0x0123456789abcdef,
		},
		b: []byte{
			0x1a,                                           // Type (i) = 0x1a,
			0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, // Data (64),
		},
	}, {
		s: "PATH_RESPONSE Data=0123456789abcdef",
		f: debugFramePathResponse{
			data: 0x0123456789abcdef,
		},
		b: []byte{
			0x1b,                                           // Type (i) = 0x1b,
			0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, // Data (64),
		},
	}, {
		s: `CONNECTION_CLOSE Code=INTERNAL_ERROR FrameType=2 Reason="oops"`,
		f: debugFrameConnectionCloseTransport{
			code:      1,
			frameType: 2,
			reason:    "oops",
		},
		b: []byte{
			0x1c,               // Type (i) = 0x1c..0x1d,
			0x01,               // Error Code (i),
			0x02,               // [Frame Type (i)],
			0x04,               // Reason Phrase Length (i),
			'o', 'o', 'p', 's', // Reason Phrase (..),
		},
	}, {
		s: `CONNECTION_CLOSE AppCode=1 Reason="oops"`,
		f: debugFrameConnectionCloseApplication{
			code:   1,
			reason: "oops",
		},
		b: []byte{
			0x1d,               // Type (i) = 0x1c..0x1d,
			0x01,               // Error Code (i),
			0x04,               // Reason Phrase Length (i),
			'o', 'o', 'p', 's', // Reason Phrase (..),
		},
	}, {
		s: "HANDSHAKE_DONE",
		f: debugFrameHandshakeDone{},
		b: []byte{
			0x1e, // Type (i) = 0x1e,
		},
	}} {
		var w packetWriter
		w.reset(1200)
		w.start1RTTPacket(0, 0, nil)
		w.pktLim = w.payOff + len(test.b)
		if added := test.f.write(&w); !added {
			t.Errorf("encoding %v with %v bytes available: write unexpectedly failed", test.f, len(test.b))
		}
		if got, want := w.payload(), test.b; !bytes.Equal(got, want) {
			t.Errorf("encoding %v:\ngot  {%x}\nwant {%x}", test.f, got, want)
		}
		gotf, n := parseDebugFrame(test.b)
		if n != len(test.b) || !reflect.DeepEqual(gotf, test.f) {
			t.Errorf("decoding {%x}:\ndecoded %v bytes, want %v\ngot:  %v\nwant: %v", test.b, n, len(test.b), gotf, test.f)
		}
		if got, want := test.f.String(), test.s; got != want {
			t.Errorf("frame.String():\ngot  %q\nwant %q", got, want)
		}

		// Try encoding the frame into too little space.
		// Most frames will result in an error; some (like STREAM frames) will truncate
		// the data written.
		w.reset(1200)
		w.start1RTTPacket(0, 0, nil)
		w.pktLim = w.payOff + len(test.b) - 1
		if added := test.f.write(&w); added {
			if test.truncated == nil {
				t.Errorf("encoding %v with %v-1 bytes available: write unexpectedly succeeded", test.f, len(test.b))
			} else if got, want := w.payload(), test.truncated; !bytes.Equal(got, want) {
				t.Errorf("encoding %v with %v-1 bytes available:\ngot  {%x}\nwant {%x}", test.f, len(test.b), got, want)
			}
		}

		// Try parsing truncated data.
		for i := 0; i < len(test.b); i++ {
			f, n := parseDebugFrame(test.b[:i])
			if n >= 0 {
				t.Errorf("decoding truncated frame {%x}:\ngot: %v\nwant error", test.b[:i], f)
			}
		}
	}
}

func TestFrameDecode(t *testing.T) {
	for _, test := range []struct {
		desc string
		want debugFrame
		b    []byte
	}{{
		desc: "STREAM frame with LEN bit unset",
		want: debugFrameStream{
			id:   1,
			fin:  false,
			data: []byte{0x01, 0x02, 0x03},
		},
		b: []byte{
			0x08, // Type (i) = 0x08..0x0f,
			0x01, // Stream ID (i),
			// [Offset (i)],
			// [Length (i)],
			0x01, 0x02, 0x03, // Stream Data (..),
		},
	}, {
		desc: "ACK frame with ECN counts",
		want: debugFrameAck{
			ackDelay: (10 << ackDelayExponent) * time.Microsecond,
			ranges: []i64range{
				{0, 1},
			},
		},
		b: []byte{
			0x03,             // TYPE (i) = 0x02..0x03
			0x00,             // Largest Acknowledged (i)
			10,               // ACK Delay (i)
			0x00,             // ACK Range Count (i)
			0x00,             // First ACK Range (i)
			0x01, 0x02, 0x03, // [ECN Counts (..)],
		},
	}} {
		got, n := parseDebugFrame(test.b)
		if n != len(test.b) || !reflect.DeepEqual(got, test.want) {
			t.Errorf("decoding {%x}:\ndecoded %v bytes, want %v\ngot:  %v\nwant: %v", test.b, n, len(test.b), got, test.want)
		}
	}
}

func TestFrameDecodeErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		b    []byte
	}{{
		name: "ACK [-1,0]",
		b: []byte{
			0x02, // TYPE (i) = 0x02
			0x00, // Largest Acknowledged (i)
			0x00, // ACK Delay (i)
			0x00, // ACK Range Count (i)
			0x01, // First ACK Range (i)
		},
	}, {
		name: "ACK [-1,16]",
		b: []byte{
			0x02, // TYPE (i) = 0x02
			0x10, // Largest Acknowledged (i)
			0x00, // ACK Delay (i)
			0x00, // ACK Range Count (i)
			0x11, // First ACK Range (i)
		},
	}, {
		name: "ACK [-1,0],[1,2]",
		b: []byte{
			0x02, // TYPE (i) = 0x02
			0x02, // Largest Acknowledged (i)
			0x00, // ACK Delay (i)
			0x01, // ACK Range Count (i)
			0x00, // First ACK Range (i)
			0x01, // Gap (i)
			0x01, // ACK Range Length (i)
		},
	}, {
		name: "NEW_TOKEN with zero-length token",
		b: []byte{
			0x07, // Type (i) = 0x07,
			0x00, // Token Length (i),
		},
	}, {
		name: "MAX_STREAMS with too many streams",
		b: func() []byte {
			// https://www.rfc-editor.org/rfc/rfc9000.html#section-19.11-5.2.1
			return appendVarint([]byte{frameTypeMaxStreamsBidi}, (1<<60)+1)
		}(),
	}, {
		name: "NEW_CONNECTION_ID too small",
		b: []byte{
			0x18, // Type (i) = 0x18,
			0x03, // Sequence Number (i),
			0x02, // Retire Prior To (i),
			0x00, // Length (8),
			// Connection ID (8..160),
			1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, // Stateless Reset Token (128),
		},
	}, {
		name: "NEW_CONNECTION_ID too large",
		b: []byte{
			0x18, // Type (i) = 0x18,
			0x03, // Sequence Number (i),
			0x02, // Retire Prior To (i),
			21,   // Length (8),
			1, 2, 3, 4, 5, 6, 7, 8, 9, 10,
			11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, // Connection ID (8..160),
			1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, // Stateless Reset Token (128),
		},
	}, {
		name: "NEW_CONNECTION_ID sequence smaller than retire",
		b: []byte{
			0x18,       // Type (i) = 0x18,
			0x02,       // Sequence Number (i),
			0x03,       // Retire Prior To (i),
			0x02,       // Length (8),
			0xff, 0xff, // Connection ID (8..160),
			1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, // Stateless Reset Token (128),
		},
	}} {
		f, n := parseDebugFrame(test.b)
		if n >= 0 {
			t.Errorf("%v: no error when parsing invalid frame {%x}\ngot: %v", test.name, test.b, f)
		}
	}
}

func FuzzParseLongHeaderPacket(f *testing.F) {
	cid := unhex(`0000000000000000`)
	_, initialServerKeys := initialKeys(cid)
	f.Fuzz(func(t *testing.T, in []byte) {
		parseLongHeaderPacket(in, initialServerKeys, 0)
	})
}

func FuzzFrameDecode(f *testing.F) {
	f.Fuzz(func(t *testing.T, in []byte) {
		parseDebugFrame(in)
	})
}
