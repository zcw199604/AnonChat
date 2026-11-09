package persona

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// parsePNG 逐块读取元数据，长度与 CRC 校验均在取片之前完成。
func parsePNG(data []byte) (Result, error) {
	var chara, ccv3 []byte
	ended := false
	for off := len(pngSignature); off < len(data); {
		if len(data)-off < 12 {
			return Result{}, errors.New("PNG chunk 长度错误")
		}
		n := uint64(binary.BigEndian.Uint32(data[off : off+4]))
		if n > uint64(len(data)-off-12) {
			return Result{}, errors.New("PNG chunk 长度错误")
		}
		size := int(n)
		kind := string(data[off+4 : off+8])
		payload := data[off+8 : off+8+size]
		if crc32.ChecksumIEEE(data[off+4:off+8+size]) != binary.BigEndian.Uint32(data[off+8+size:off+12+size]) {
			return Result{}, errors.New("PNG chunk 校验失败")
		}
		off += 12 + size
		if kind == "IEND" {
			if size != 0 {
				return Result{}, errors.New("PNG 结束块错误")
			}
			ended = true
			break
		}
		if kind != "tEXt" && kind != "iTXt" {
			continue
		}
		if size > maxJSONBytes {
			return Result{}, errors.New("PNG 文本块不能超过 5 MB")
		}
		sep := bytes.IndexByte(payload, 0)
		if sep < 0 {
			continue
		}
		key := string(payload[:sep])
		if key != "chara" && key != "ccv3" {
			continue
		}
		text := payload[sep+1:]
		if kind == "iTXt" {
			if len(text) < 2 || text[0] != 0 || text[1] != 0 {
				return Result{}, errors.New("只支持未压缩的 PNG iTXt 文本")
			}
			text = text[2:]
			for i := 0; i < 2; i++ {
				end := bytes.IndexByte(text, 0)
				if end < 0 {
					return Result{}, errors.New("PNG iTXt 文本格式错误")
				}
				text = text[end+1:]
			}
		}
		if key == "ccv3" {
			ccv3 = text
		} else {
			chara = text
		}
	}
	if !ended {
		return Result{}, errors.New("PNG 缺少结束块")
	}
	text := ccv3
	if text == nil {
		text = chara
	}
	if text == nil {
		return Result{}, errors.New("PNG 中没有角色卡元数据")
	}
	decoded, err := base64.StdEncoding.DecodeString(string(text))
	if err != nil {
		return Result{}, errors.New("PNG 角色卡元数据不是有效的 base64")
	}
	return parseST(decoded)
}
