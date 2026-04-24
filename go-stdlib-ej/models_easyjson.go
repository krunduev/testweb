package main

import "github.com/mailru/easyjson/jwriter"

func (v PingResponse) MarshalJSON() ([]byte, error) {
	w := jwriter.Writer{}
	w.RawByte('{')
	w.RawString(`"status":`)
	w.String(v.Status)
	w.RawString(`,"service":`)
	w.String(v.Service)
	w.RawByte('}')
	return w.Buffer.BuildBytes(), w.Error
}

func (v FibResponse) MarshalJSON() ([]byte, error) {
	w := jwriter.Writer{}
	w.RawByte('{')
	w.RawString(`"n":`)
	w.Int(v.N)
	w.RawString(`,"result":`)
	w.Int64(v.Result)
	w.RawString(`,"time_ms":`)
	w.Float64(v.TimeMs)
	w.RawByte('}')
	return w.Buffer.BuildBytes(), w.Error
}

func (v AllocResponse) MarshalJSON() ([]byte, error) {
	w := jwriter.Writer{}
	w.RawByte('{')
	w.RawString(`"requested_bytes":`)
	w.Int(v.RequestedBytes)
	w.RawString(`,"alloced_mb":`)
	w.Float64(v.AllocedMB)
	w.RawByte('}')
	return w.Buffer.BuildBytes(), w.Error
}

func (v MetricsResponse) MarshalJSON() ([]byte, error) {
	w := jwriter.Writer{}
	w.RawByte('{')
	w.RawString(`"mem_mb":`)
	w.Float64(v.MemMB)
	w.RawString(`,"threads":`)
	w.Int(v.Threads)
	w.RawByte('}')
	return w.Buffer.BuildBytes(), w.Error
}
