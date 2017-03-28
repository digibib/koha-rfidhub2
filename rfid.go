package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// RFIDState represent the current state of a RFID-reader.
type RFIDState int

// Possible RFIDStates
const (
	RFIDIdle RFIDState = iota
	RFIDCheckinWaitForBegOK
	RFIDCheckin
	RFIDCheckout
	RFIDCheckoutWaitForBegOK
	RFIDWaitForCheckinAlarmOn
	RFIDWaitForCheckinAlarmLeave
	RFIDWaitForCheckoutAlarmOff
	RFIDWaitForCheckoutAlarmLeave
	RFIDPreWriteStep1
	RFIDPreWriteStep2
	RFIDPreWriteStep3
	RFIDPreWriteStep4
	RFIDPreWriteStep5
	RFIDPreWriteStep6
	RFIDPreWriteStep7
	RFIDPreWriteStep8
	RFIDWriting
	RFIDWaitForTagCount
	RFIDWaitForRetryAlarmOn
	RFIDWaitForRetryAlarmOff
	RFIDWaitForEndOK
)

type RFIDCommand int

const (
	cmdInitVersion RFIDCommand = iota
	cmdBeginScan
	cmdEndScan
	cmdRereadTag
	cmdAlarmOn
	cmdRetryAlarmOn
	cmdAlarmOff
	cmdRetryAlarmOff
	cmdAlarmLeave
	cmdTagCount
	cmdWrite

	// Initialize writer commands.
	// SLP (Set Library Parameter) commands. Reader returns OK or NOK.
	cmdSLPLBN // SLPLBN|02030000 (LBN: library number)
	cmdSLPLBC // SLPLBC|NO       (LBC: library country code)
	cmdSLPDTM // SLPDTM|DS24     (DTM: data model, "Danish Standard" / ISO28560−3)
	cmdSLPSSB // SLPSSB|0        (SSB: set security bit when writing, 0: Reset, 1: Set)
	cmdSLPCRD // SLPCRD|1        (CRD: check read after write: 0: No, 1: Yes)
	cmdSLPWTM // SLPWTM|5000     (WTM: time to wait for "setsize" tags to be available, in ms)
	cmdSLPRSS // SLPRSS|1        (RSS: return set status, status value for 1−tag−only sets,
	//                  0: complete, 1: not complete, 2: complete but check manually)

	// The following are not used:
	//cmdSLPEID // SLPEID|1        (EID: send extended ID, 0: No, 1: Yes − include library number and country code)
	//cmdSLPESP // SLPESP|:        (ESP: extended ID separator: default character ’:’)
)

type RFIDManager struct {
	buf       bytes.Buffer
	WriteMode bool
}

func newRFIDManager() *RFIDManager {
	return &RFIDManager{}
}

func (v *RFIDManager) Reset() {
	v.WriteMode = false
}

// GenRequest genereates a RFID request.
func (v *RFIDManager) GenRequest(r RFIDReq) []byte {
	switch r.Cmd {
	case cmdInitVersion:
		return []byte("VER2.00\r")
	case cmdBeginScan:
		return []byte("BEG\r")
	case cmdEndScan:
		return []byte("END\r")
	case cmdAlarmLeave:
		return []byte("OK \r")
	case cmdAlarmOff:
		return []byte("OK0\r")
	case cmdAlarmOn:
		return []byte("OK1\r")
	case cmdRetryAlarmOn:
		v.buf.Reset()
		v.buf.Write([]byte("ACT"))
		v.buf.Write(r.Data)
		v.buf.WriteByte('\r')
		return v.buf.Bytes()
	case cmdRetryAlarmOff:
		v.buf.Reset()
		v.buf.Write([]byte("DAC"))
		v.buf.Write(r.Data)
		v.buf.WriteByte('\r')
		return v.buf.Bytes()
	case cmdRereadTag:
		return []byte("OKR\r")
	case cmdTagCount:
		return []byte("TGC\r")
	case cmdWrite:
		v.WriteMode = true
		v.buf.Reset()
		i := strconv.Itoa(r.TagCount)
		v.buf.Write([]byte("WRT")) // Write Tag
		v.buf.Write(r.Data)
		v.buf.WriteByte('|')
		// Number of parts in set
		v.buf.WriteString(i)
		// 0: multipart sets have a tag on each part
		// 1: single tag only
		v.buf.Write([]byte("|0\r"))
		return v.buf.Bytes()
	case cmdSLPLBN:
		return []byte("SLPLBN|02030000\r")
	case cmdSLPLBC:
		return []byte("SLPLBC|NO\r")
	case cmdSLPDTM:
		return []byte("SLPDTM|DS24\r")
	case cmdSLPSSB:
		return []byte("SLPSSB|0\r")
	case cmdSLPCRD:
		return []byte("SLPCRD|1\r")
	case cmdSLPWTM:
		return []byte("SLPWTM|5000\r")
	case cmdSLPRSS:
		return []byte("SLPRSS|1\r")
	default:
		// All cases handled above
		panic("unexpected RFIDcommand")
	}
}

// ParseResponse parses a RFID response.
func (v *RFIDManager) ParseResponse(r []byte) (RFIDResp, error) {
	// TODO this parser function could use some love
	s := strings.TrimSuffix(string(r), "\r")
	s = strings.TrimPrefix(s, "\n")
	l := len(s)

	switch {
	case l == 2:
		if s == "OK" {
			return RFIDResp{OK: true}, nil
		}
	case l == 3:
		if s == "NOK" {
			return RFIDResp{OK: false}, nil
		}
	case l > 3:
		if s[0:2] == "OK" {
			b := strings.Split(s, "|")
			if len(b) <= 1 {
				break
			}
			if v.WriteMode {
				// Ex: OK|E004010046A847AD|E004010046A847AD
				return RFIDResp{OK: true, WrittenIDs: b[1:]}, nil
			}
			// Ex: OK|2
			i, err := strconv.Atoi(b[1])
			if err != nil {
				break
			}
			return RFIDResp{OK: true, TagCount: i}, nil
		}
		if s[0:3] == "RDT" {
			b := strings.Split(s[3:l], "|")
			if len(b) <= 1 {
				break
			}
			var ok bool
			if b[1] == "0" {
				ok = true
			}
			if b[1] != "0" && b[1] != "1" {
				break
			}
			t := strings.Split(b[0], ":")
			return RFIDResp{OK: ok, Tag: b[0], Barcode: t[0]}, nil
		}
		if s[0:3] == "NOK" {
			b := strings.Split(s[3:l], "|")
			if len(b) <= 1 {
				break
			}
			i, err := strconv.Atoi(b[1])
			if err != nil {
				break
			}
			return RFIDResp{OK: false, TagCount: i}, nil
		}
	}

	// Fall-through case:
	return RFIDResp{}, fmt.Errorf("cannot parse RFID response: %q", r)
}

// RFIDReq represents request to be sent to the RFID-unit.
type RFIDReq struct {
	Cmd      RFIDCommand
	Data     []byte
	TagCount int
}

// RFIDResp represents a parsed response from the RFID-unit.
type RFIDResp struct {
	OK         bool
	TagCount   int
	Tag        string // 1003010530352001:NO:02030000
	Barcode    string // 1003010530352001
	WrittenIDs []string
}
