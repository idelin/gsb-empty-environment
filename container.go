package lcc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
)

func Encode(raw []byte, layers int, limits Limits) ([]byte, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}
	if layers < 1 || layers > limits.MaxLayers {
		return nil, fmt.Errorf("layer count %d is outside [1,%d]", layers, limits.MaxLayers)
	}

	current := raw
	innerToOuter := make([]layerRecord, layers)
	for i := 0; i < layers; i++ {
		if uint64(len(current)) > maxPayloadSize {
			return nil, fmt.Errorf("layer %d input length exceeds uint32 length field", i)
		}
		encoded := encodeRLE(current)
		if uint64(len(encoded)) > maxPayloadSize {
			return nil, fmt.Errorf("layer %d encoded length exceeds uint32 length field", i)
		}
		innerToOuter[i] = layerRecord{
			codec:      codecRLE,
			encodedLen: uint32(len(encoded)),
			decodedLen: uint32(len(current)),
		}
		current = encoded
	}

	records := make([]layerRecord, len(innerToOuter))
	for i := range innerToOuter {
		records[i] = innerToOuter[len(innerToOuter)-1-i]
	}
	payload := current
	if err := checkPlan(limits, records, uint64(len(payload))); err != nil {
		return nil, err
	}

	header, err := makeHeader(len(records), uint64(len(payload)))
	if err != nil {
		return nil, err
	}
	table := marshalRecords(records)
	container := make([]byte, 0, len(header)+len(table)+len(payload)+4)
	container = append(container, header...)
	container = append(container, table...)
	container = append(container, payload...)

	checksum := crc32.Update(0, crc32.IEEETable, table)
	checksum = crc32.Update(checksum, crc32.IEEETable, payload)
	container = binary.BigEndian.AppendUint32(container, checksum)
	return container, nil
}

func Decode(container []byte, limits Limits) ([]byte, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}
	r := bytes.NewReader(container)
	var output bytes.Buffer
	p, err := prepareContainer(r, limits)
	if err != nil {
		return nil, err
	}
	if err := p.decode(r, &output, limits); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func DecodeReader(r io.Reader, w io.Writer, limits Limits) error {
	p, err := prepareContainer(r, limits)
	if err != nil {
		return err
	}
	return p.decode(r, w, limits)
}

type preparedContainer struct {
	table      []byte
	payloadLen uint64
	records    []layerRecord
}

func prepareContainer(r io.Reader, limits Limits) (*preparedContainer, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}

	header := make([]byte, headerSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("read container header: %w", err)
	}
	if !bytes.Equal(header[0:4], magic) {
		return nil, errors.New("invalid container magic")
	}
	if binary.BigEndian.Uint16(header[4:6]) != FormatVersion {
		return nil, fmt.Errorf("unsupported format version %d", binary.BigEndian.Uint16(header[4:6]))
	}
	if binary.BigEndian.Uint32(header[12:16]) != crc32.ChecksumIEEE(header[:12]) {
		return nil, errors.New("invalid header checksum")
	}

	layerCount := int(binary.BigEndian.Uint16(header[6:8]))
	if layerCount < 1 || layerCount > limits.MaxLayers {
		return nil, fmt.Errorf("layer count %d is outside [1,%d]", layerCount, limits.MaxLayers)
	}
	table := make([]byte, layerCount*recordSize)
	if _, err := io.ReadFull(r, table); err != nil {
		return nil, fmt.Errorf("read layer table: %w", err)
	}

	payloadLen := uint64(binary.BigEndian.Uint32(header[8:12]))
	records, err := parseRecords(table, layerCount)
	if err != nil {
		return nil, err
	}
	if err := checkPlan(limits, records, payloadLen); err != nil {
		return nil, err
	}
	if payloadLen > uint64(int(^uint(0)>>1)) {
		return nil, errors.New("payload length exceeds platform allocation limit")
	}
	return &preparedContainer{table: table, payloadLen: payloadLen, records: records}, nil
}

func (p *preparedContainer) decode(r io.Reader, w io.Writer, limits Limits) error {
	table := p.table
	records := p.records
	payloadLen := p.payloadLen

	payload := make([]byte, int(payloadLen))
	if _, err := io.ReadFull(r, payload); err != nil {
		return fmt.Errorf("read payload: %w", err)
	}
	trailer := make([]byte, 4)
	if _, err := io.ReadFull(r, trailer); err != nil {
		return fmt.Errorf("read payload checksum: %w", err)
	}
	checksum := crc32.Update(0, crc32.IEEETable, table)
	checksum = crc32.Update(checksum, crc32.IEEETable, payload)
	if binary.BigEndian.Uint32(trailer) != checksum {
		return errors.New("invalid payload checksum")
	}
	var trailing [1]byte
	switch n, err := r.Read(trailing[:]); {
	case n > 0:
		return errors.New("unexpected trailing bytes")
	case err != io.EOF:
		return fmt.Errorf("check end of container: %w", err)
	}

	current := payload
	inputRemaining := limits.MaxInputBytes
	outputRemaining := limits.MaxOutputBytes
	for i, record := range records {
		wantInput := int64(record.encodedLen)
		if err := reserve(wantInput, inputRemaining, "shared input"); err != nil {
			return err
		}
		inputRemaining -= wantInput

		wantOutput := int64(record.decodedLen)
		if err := reserve(wantOutput, outputRemaining, "shared output"); err != nil {
			return err
		}
		availableOutput := outputRemaining
		outputRemaining -= wantOutput

		if len(current) != int(record.encodedLen) {
			return fmt.Errorf("layer %d encoded length mismatch", i)
		}
		if i == len(records)-1 {
			dst := &budgetWriter{w: w, remaining: availableOutput}
			if err := decodeRLE(current, dst, record.decodedLen); err != nil {
				return fmt.Errorf("decode layer %d: %w", i, err)
			}
			continue
		}

		next := make([]byte, record.decodedLen)
		dst := &sliceWriter{buf: next}
		if err := decodeRLE(current, dst, record.decodedLen); err != nil {
			return fmt.Errorf("decode layer %d: %w", i, err)
		}
		current = next
	}
	return nil
}

func DecodeFile(containerPath, outputPath string, limits Limits) (err error) {
	input, err := os.Open(containerPath)
	if err != nil {
		return err
	}
	defer input.Close()

	p, err := prepareContainer(input, limits)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(outputPath), ".lcc-output-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = temp.Close()
			_ = os.Remove(tempName)
		}
	}()

	if err := p.decode(input, temp, limits); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, outputPath); err != nil {
		return err
	}
	succeeded = true
	return nil
}
