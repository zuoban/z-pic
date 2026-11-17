package compress

import "bytes"

type jpegOutput struct {
	buf       bytes.Buffer
	limit     int
	discarded bool
}

func (output *jpegOutput) Write(data []byte) (int, error) {
	if output.discarded || len(data) == 0 {
		return len(data), nil
	}
	if len(data) >= output.limit-output.buf.Len() {
		output.buf = bytes.Buffer{}
		output.discarded = true
		return len(data), nil
	}
	return output.buf.Write(data)
}
