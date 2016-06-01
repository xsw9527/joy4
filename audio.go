package ffmpeg

import (
	/*
	#include "ffmpeg.h"
	int wrap_avcodec_decode_audio4(AVCodecContext *ctx, AVFrame *frame, void *data, int size, int *got) {
		struct AVPacket pkt = {.data = data, .size = size};
		return avcodec_decode_audio4(ctx, frame, got, &pkt);
	}
	*/
	"C"
	"unsafe"
	"runtime"
	"fmt"
	"github.com/nareix/av"
	"github.com/nareix/codec/aacparser"
)

type ffctx struct {
	ff C.FFCtx
}

type Resampler struct {
	inSampleFormat, OutSampleFormat av.SampleFormat
	inChannelLayout, OutChannelLayout av.ChannelLayout
	inSampleRate, OutSampleRate int
	avr *C.AVAudioResampleContext
}

func (self *Resampler) Resample(in av.AudioFrame) (out av.AudioFrame, err error) {
	formatChange := in.SampleRate != self.inSampleRate || in.SampleFormat != self.inSampleFormat || in.ChannelLayout != self.inChannelLayout

	var flush av.AudioFrame

	if formatChange {
		if self.avr != nil {
			outChannels := self.OutChannelLayout.Count()
			if !self.OutSampleFormat.IsPlanar() {
				outChannels = 1
			}
			outData := make([]*C.uint8_t, outChannels)
			outSampleCount := int(C.avresample_get_out_samples(self.avr, C.int(in.SampleCount)))
			outLinesize := outSampleCount*self.OutSampleFormat.BytesPerSample()
			flush.Data = make([][]byte, outChannels)
			for i := 0; i < outChannels; i++ {
				flush.Data[i] = make([]byte, outLinesize)
				outData[i] = (*C.uint8_t)(unsafe.Pointer(&flush.Data[i][0]))
			}
			flush.ChannelLayout = self.OutChannelLayout
			flush.SampleFormat = self.OutSampleFormat
			flush.SampleRate = self.OutSampleRate

			convertSamples := int(C.avresample_convert(
				self.avr,
				unsafe.Pointer(&outData[0]), C.int(outLinesize), C.int(outSampleCount),
				nil, C.int(0), C.int(0),
			))
			if convertSamples < 0 {
				err = fmt.Errorf("avresample_convert_frame failed")
				return
			}
			flush.SampleCount = convertSamples
			if convertSamples < outSampleCount {
				for i := 0; i < outChannels; i++ {
					flush.Data[i] = flush.Data[i][:convertSamples*self.OutSampleFormat.BytesPerSample()]
				}
			}

			//fmt.Println("flush:", "outSampleCount", outSampleCount, "convertSamples", convertSamples, "datasize", len(flush.Data[0]))
		} else {
			runtime.SetFinalizer(self, func(self *Resampler) {
				self.Close()
			})
		}

		C.avresample_free(&self.avr)
		self.inSampleFormat = in.SampleFormat
		self.inSampleRate = in.SampleRate
		self.inChannelLayout = in.ChannelLayout
		avr := C.avresample_alloc_context()
		C.av_opt_set_int(unsafe.Pointer(avr), C.CString("in_channel_layout"), C.int64_t(channelLayoutAV2FF(self.inChannelLayout)), 0)
		C.av_opt_set_int(unsafe.Pointer(avr), C.CString("out_channel_layout"), C.int64_t(channelLayoutAV2FF(self.OutChannelLayout)), 0)
		C.av_opt_set_int(unsafe.Pointer(avr), C.CString("in_sample_rate"), C.int64_t(self.inSampleRate), 0)
		C.av_opt_set_int(unsafe.Pointer(avr), C.CString("out_sample_rate"), C.int64_t(self.OutSampleRate), 0)
		C.av_opt_set_int(unsafe.Pointer(avr), C.CString("in_sample_fmt"), C.int64_t(sampleFormatAV2FF(self.inSampleFormat)), 0)
		C.av_opt_set_int(unsafe.Pointer(avr), C.CString("out_sample_fmt"), C.int64_t(sampleFormatAV2FF(self.OutSampleFormat)), 0)
		C.avresample_open(avr)
		self.avr = avr
	}

	inChannels := self.inChannelLayout.Count()
	if !self.inSampleFormat.IsPlanar() {
		inChannels = 1
	}
	inSampleCount := in.SampleCount
	inLinesize := inSampleCount*in.SampleFormat.BytesPerSample()
	inData := make([]*C.uint8_t, inChannels)
	for i := 0; i < inChannels; i++ {
		inData[i] = (*C.uint8_t)(unsafe.Pointer(&in.Data[i][0]))
	}

	outChannels := self.OutChannelLayout.Count()
	if !self.OutSampleFormat.IsPlanar() {
		outChannels = 1
	}
	outData := make([]*C.uint8_t, outChannels)
	outSampleCount := int(C.avresample_get_out_samples(self.avr, C.int(in.SampleCount)))
	outLinesize := outSampleCount*self.OutSampleFormat.BytesPerSample()
	out.Data = make([][]byte, outChannels)
	for i := 0; i < outChannels; i++ {
		out.Data[i] = make([]byte, outLinesize)
		outData[i] = (*C.uint8_t)(unsafe.Pointer(&out.Data[i][0]))
	}
	out.ChannelLayout = self.OutChannelLayout
	out.SampleFormat = self.OutSampleFormat
	out.SampleRate = self.OutSampleRate

	convertSamples := int(C.avresample_convert(
		self.avr,
		unsafe.Pointer(&outData[0]), C.int(outLinesize), C.int(outSampleCount),
		unsafe.Pointer(&inData[0]), C.int(inLinesize), C.int(inSampleCount),
	))
	if convertSamples < 0 {
		err = fmt.Errorf("avresample_convert_frame failed")
		return
	}
	out.SampleCount = convertSamples
	if convertSamples < outSampleCount {
		for i := 0; i < outChannels; i++ {
			out.Data[i] = out.Data[i][:convertSamples*self.OutSampleFormat.BytesPerSample()]
		}
	}
	//fmt.Println("outSampleCount", outSampleCount, "convertSamples", convertSamples)

	if flush.SampleCount > 0 {
		out = flush.Concat(out)
	}

	return
}

func (self *Resampler) Close() {
	C.avresample_free(&self.avr)
}

type AudioEncoder struct {
	ff *ffctx
	SampleRate int
	BitRate int
	ChannelLayout av.ChannelLayout
	SampleFormat av.SampleFormat
	FrameSampleCount int
	framebuf av.AudioFrame
	codecData av.AudioCodecData
	resampler *Resampler
}

func sampleFormatAV2FF(sampleFormat av.SampleFormat) (ffsamplefmt int32) {
	switch sampleFormat {
	case av.U8:
		ffsamplefmt = C.AV_SAMPLE_FMT_U8
	case av.S16:
		ffsamplefmt = C.AV_SAMPLE_FMT_S16
	case av.S32:
		ffsamplefmt = C.AV_SAMPLE_FMT_S32
	case av.FLT:
		ffsamplefmt = C.AV_SAMPLE_FMT_FLT
	case av.DBL:
		ffsamplefmt = C.AV_SAMPLE_FMT_DBL
	case av.U8P:
		ffsamplefmt = C.AV_SAMPLE_FMT_U8P
	case av.S16P:
		ffsamplefmt = C.AV_SAMPLE_FMT_S16P
	case av.S32P:
		ffsamplefmt = C.AV_SAMPLE_FMT_S32P
	case av.FLTP:
		ffsamplefmt = C.AV_SAMPLE_FMT_FLTP
	case av.DBLP:
		ffsamplefmt = C.AV_SAMPLE_FMT_DBLP
	}
	return
}

func sampleFormatFF2AV(ffsamplefmt int32) (sampleFormat av.SampleFormat) {
	switch ffsamplefmt {
	case C.AV_SAMPLE_FMT_U8:          ///< unsigned 8 bits
		sampleFormat = av.U8
	case C.AV_SAMPLE_FMT_S16:         ///< signed 16 bits
		sampleFormat = av.S16
	case C.AV_SAMPLE_FMT_S32:         ///< signed 32 bits
		sampleFormat = av.S32
	case C.AV_SAMPLE_FMT_FLT:         ///< float
		sampleFormat = av.FLT
	case C.AV_SAMPLE_FMT_DBL:         ///< double
		sampleFormat = av.DBL
	case C.AV_SAMPLE_FMT_U8P:         ///< unsigned 8 bits, planar
		sampleFormat = av.U8P
	case C.AV_SAMPLE_FMT_S16P:        ///< signed 16 bits, planar
		sampleFormat = av.S16P
	case C.AV_SAMPLE_FMT_S32P:        ///< signed 32 bits, planar
		sampleFormat = av.S32P
	case C.AV_SAMPLE_FMT_FLTP:        ///< float, planar
		sampleFormat = av.FLTP
	case C.AV_SAMPLE_FMT_DBLP:        ///< double, planar
		sampleFormat = av.DBLP
	}
	return
}

func (self *AudioEncoder) Setup() (err error) {
	ff := &self.ff.ff

	ff.frame = C.av_frame_alloc()
	if self.SampleFormat == av.SampleFormat(0) {
		self.SampleFormat = av.FLTP
	}
	if self.BitRate == 0 {
		self.BitRate = 50000
	}
	if self.SampleRate == 0 {
		self.SampleRate = 44100
	}
	if self.ChannelLayout == av.ChannelLayout(0) {
		self.ChannelLayout = av.CH_STEREO
	}

	ff.codecCtx.sample_fmt = sampleFormatAV2FF(self.SampleFormat)
	ff.codecCtx.sample_rate = C.int(self.SampleRate)
	ff.codecCtx.bit_rate = C.int(self.BitRate)
	ff.codecCtx.channel_layout = channelLayoutAV2FF(self.ChannelLayout)
	ff.codecCtx.strict_std_compliance = C.FF_COMPLIANCE_EXPERIMENTAL
	if C.avcodec_open2(ff.codecCtx, ff.codec, nil) != 0 {
		err = fmt.Errorf("avcodec_open2 failed")
		return
	}
	self.SampleFormat = sampleFormatFF2AV(ff.codecCtx.sample_fmt)
	self.FrameSampleCount = int(ff.codecCtx.frame_size)

	extradata := C.GoBytes(unsafe.Pointer(ff.codecCtx.extradata), ff.codecCtx.extradata_size)
	switch ff.codecCtx.codec_id {
	case C.AV_CODEC_ID_AAC:
		if self.codecData, err = aacparser.NewCodecDataFromMPEG4AudioConfigBytes(extradata); err != nil {
			return
		}

	default:
		self.codecData = AudioCodecData{
			channelLayout: self.ChannelLayout,
			sampleFormat: self.SampleFormat,
			sampleRate: self.SampleRate,
			codecId: ff.codecCtx.codec_id,
			extradata: extradata,
		}
	}

	return
}

func (self *AudioEncoder) CodecData() (codec av.AudioCodecData) {
	return self.codecData
}

func (self *AudioEncoder) encodeOne(frame av.AudioFrame) (gotpkt bool, pkt av.Packet, err error) {
	ff := &self.ff.ff

	if ff.frame == nil {
		if err = self.Setup(); err != nil {
			return
		}
	}

	cpkt := C.AVPacket{}
	cgotpkt := C.int(0)
	audioFrameAssignToFF(frame, ff.frame)

	if false {
		farr := []string{}
		for i := 0; i < len(frame.Data[0])/4; i++ {
			var f *float64 = (*float64)(unsafe.Pointer(&frame.Data[0][i*4]))
			farr = append(farr, fmt.Sprintf("%.8f", *f))
		}
		fmt.Println(farr)
	}
	//fmt.Println("encode", ff.frame.channels, ff.frame.nb_samples, ff.frame.sample_rate, ff.frame.channel_layout, ff.frame.linesize[0], len(frame.Data))

	cerr := C.avcodec_encode_audio2(ff.codecCtx, &cpkt, ff.frame, &cgotpkt)
	if cerr < C.int(0) {
		err = fmt.Errorf("avcodec_encode_audio2 failed: %d", cerr)
		return
	}

	if cgotpkt != 0 {
		gotpkt = true
		pkt.Data = C.GoBytes(unsafe.Pointer(cpkt.data), cpkt.size)
		pkt.Duration = float64(frame.SampleCount)/float64(self.SampleRate)
		C.av_free_packet(&cpkt)
	}

	return
}

func (self *AudioEncoder) resample(in av.AudioFrame) (out av.AudioFrame, err error) {
	if self.resampler == nil {
		self.resampler = &Resampler{
			OutSampleFormat: self.SampleFormat,
			OutSampleRate: self.SampleRate,
			OutChannelLayout: self.ChannelLayout,
		}
	}
	if out, err = self.resampler.Resample(in); err != nil {
		return
	}
	return
}

func (self *AudioEncoder) Encode(frame av.AudioFrame) (pkts []av.Packet, err error) {
	var gotpkt bool
	var pkt av.Packet

	if frame.SampleFormat != self.SampleFormat || frame.ChannelLayout != self.ChannelLayout || frame.SampleRate != self.SampleRate {
		if frame, err = self.resample(frame); err != nil {
			return
		}
	}

	if self.FrameSampleCount != 0 {
		if self.framebuf.SampleCount == 0 {
			self.framebuf = frame
		} else {
			self.framebuf = self.framebuf.Concat(frame)
		}
		for self.framebuf.SampleCount >= self.FrameSampleCount {
			frame := self.framebuf.Slice(0, self.FrameSampleCount)
			if gotpkt, pkt, err = self.encodeOne(frame); err != nil {
				return
			}
			if gotpkt {
				pkts = append(pkts, pkt)
			}
			self.framebuf = self.framebuf.Slice(self.FrameSampleCount, self.framebuf.SampleCount)
		}
	} else {
		if gotpkt, pkt, err = self.encodeOne(frame); err != nil {
			return
		}
		if gotpkt {
			pkts = append(pkts, pkt)
		}
	}

	return
}

func (self *AudioEncoder) Close() {
	freeFFCtx(self.ff)
	if self.resampler != nil {
		self.resampler.Close()
		self.resampler = nil
	}
}

func audioFrameAssignToAVParams(f *C.AVFrame, frame *av.AudioFrame) {
	frame.SampleFormat = sampleFormatFF2AV(int32(f.format))
	frame.ChannelLayout = channelLayoutFF2AV(f.channel_layout)
	frame.SampleRate = int(f.sample_rate)
}

func audioFrameAssignToAVData(f *C.AVFrame, frame *av.AudioFrame) {
	frame.SampleCount = int(f.nb_samples)
	frame.Data = make([][]byte, int(f.channels))
	for i := 0; i < int(f.channels); i++ {
		frame.Data[i] = C.GoBytes(unsafe.Pointer(f.data[i]), f.linesize[i])
	}
}

func audioFrameAssignToAV(f *C.AVFrame, frame *av.AudioFrame) {
	audioFrameAssignToAVParams(f, frame)
	audioFrameAssignToAVData(f, frame)
}

func audioFrameAssignToFFParams(frame av.AudioFrame, f *C.AVFrame) {
	f.format = C.int(sampleFormatAV2FF(frame.SampleFormat))
	f.channel_layout = channelLayoutAV2FF(frame.ChannelLayout)
	f.sample_rate = C.int(frame.SampleRate)
	f.channels = C.int(frame.ChannelLayout.Count())
}

func audioFrameAssignToFFData(frame av.AudioFrame, f *C.AVFrame) {
	f.nb_samples = C.int(frame.SampleCount)
	for i := range frame.Data {
		f.data[i] = (*C.uint8_t)(unsafe.Pointer(&frame.Data[i][0]))
		f.linesize[i] = C.int(len(frame.Data[i]))
	}
}

func audioFrameAssignToFF(frame av.AudioFrame, f *C.AVFrame) {
	audioFrameAssignToFFParams(frame, f)
	audioFrameAssignToFFData(frame, f)
}

func channelLayoutFF2AV(layout C.uint64_t) (channelLayout av.ChannelLayout) {
	if layout & C.AV_CH_FRONT_CENTER != 0 {
		channelLayout |= av.CH_FRONT_CENTER
	}
	if layout & C.AV_CH_FRONT_LEFT != 0 {
		channelLayout |= av.CH_FRONT_LEFT
	}
	if layout & C.AV_CH_FRONT_RIGHT != 0 {
		channelLayout |= av.CH_FRONT_RIGHT
	}
	if layout & C.AV_CH_BACK_CENTER != 0 {
		channelLayout |= av.CH_BACK_CENTER
	}
	if layout & C.AV_CH_BACK_LEFT != 0 {
		channelLayout |= av.CH_BACK_LEFT
	}
	if layout & C.AV_CH_BACK_RIGHT != 0 {
		channelLayout |= av.CH_BACK_RIGHT
	}
	if layout & C.AV_CH_SIDE_LEFT != 0 {
		channelLayout |= av.CH_SIDE_LEFT
	}
	if layout & C.AV_CH_SIDE_RIGHT != 0 {
		channelLayout |= av.CH_SIDE_RIGHT
	}
	if layout & C.AV_CH_LOW_FREQUENCY != 0 {
		channelLayout |= av.CH_LOW_FREQ
	}
	return
}

func channelLayoutAV2FF(channelLayout av.ChannelLayout) (layout C.uint64_t) {
	if channelLayout & av.CH_FRONT_CENTER != 0 {
		layout |= C.AV_CH_FRONT_CENTER
	}
	if channelLayout & av.CH_FRONT_LEFT != 0 {
		layout |= C.AV_CH_FRONT_LEFT
	}
	if channelLayout & av.CH_FRONT_RIGHT != 0 {
		layout |= C.AV_CH_FRONT_RIGHT
	}
	if channelLayout & av.CH_BACK_CENTER != 0 {
		layout |= C.AV_CH_BACK_CENTER
	}
	if channelLayout & av.CH_BACK_LEFT != 0 {
		layout |= C.AV_CH_BACK_LEFT
	}
	if channelLayout & av.CH_BACK_RIGHT != 0 {
		layout |= C.AV_CH_BACK_RIGHT
	}
	if channelLayout & av.CH_SIDE_LEFT != 0 {
		layout |= C.AV_CH_SIDE_LEFT
	}
	if channelLayout & av.CH_SIDE_RIGHT != 0 {
		layout |= C.AV_CH_SIDE_RIGHT
	}
	if channelLayout & av.CH_LOW_FREQ != 0 {
		layout |= C.AV_CH_LOW_FREQUENCY
	}
	return
}

type AudioDecoder struct {
	ff *ffctx
	ChannelLayout av.ChannelLayout
	SampleFormat av.SampleFormat
	SampleRate int
	Extradata []byte
}

func (self *AudioDecoder) Setup() (err error) {
	ff := &self.ff.ff

	ff.frame = C.av_frame_alloc()

	if len(self.Extradata) > 0 {
		ff.codecCtx.extradata = (*C.uint8_t)(unsafe.Pointer(&self.Extradata[0]))
		ff.codecCtx.extradata_size = C.int(len(self.Extradata))
	}

	ff.codecCtx.sample_rate = C.int(self.SampleRate)
	ff.codecCtx.channel_layout = channelLayoutAV2FF(self.ChannelLayout)
	ff.codecCtx.channels = C.int(self.ChannelLayout.Count())
	if C.avcodec_open2(ff.codecCtx, ff.codec, nil) != 0 {
		err = fmt.Errorf("avcodec_open2 failed")
		return
	}
	self.SampleFormat = sampleFormatFF2AV(ff.codecCtx.sample_fmt)
	self.ChannelLayout = channelLayoutFF2AV(ff.codecCtx.channel_layout)
	if self.SampleRate == 0 {
		self.SampleRate = int(ff.codecCtx.sample_rate)
	}

	return
}

func (self *AudioDecoder) Decode(data []byte) (gotframe bool, frame av.AudioFrame, err error) {
	ff := &self.ff.ff

	cgotframe := C.int(0)
	cerr := C.wrap_avcodec_decode_audio4(ff.codecCtx, ff.frame, unsafe.Pointer(&data[0]), C.int(len(data)), &cgotframe)
	if cerr < C.int(0) {
		err = fmt.Errorf("avcodec_decode_audio4 failed: %d", cerr)
		return
	}

	if cgotframe != C.int(0) {
		gotframe = true
		audioFrameAssignToAV(ff.frame, &frame)
		frame.SampleRate = self.SampleRate
	}

	return
}

func (self *AudioDecoder) Close() {
	freeFFCtx(self.ff)
}

func HasEncoder(name string) bool {
	return C.avcodec_find_encoder_by_name(C.CString(name)) != nil
}

func HasDecoder(name string) bool {
	return C.avcodec_find_decoder_by_name(C.CString(name)) != nil
}

//func EncodersList() []string
//func DecodersList() []string

func newFFCtxByCodec(codec *C.AVCodec) (ff *ffctx, err error) {
	ff = &ffctx{}
	ff.ff.codec = codec
	ff.ff.codecCtx = C.avcodec_alloc_context3(codec)
	runtime.SetFinalizer(ff, freeFFCtx)
	return
}

func freeFFCtx(self *ffctx) {
	ff := &self.ff
	if ff.frame != nil {
		C.av_frame_free(&ff.frame)
		ff.frame = nil
	}
	if ff.codecCtx != nil {
		C.avcodec_close(ff.codecCtx)
		C.av_free(ff.codecCtx)
		ff.codecCtx = nil
	}
}

func NewAudioEncoderByCodecType(typ int) (enc *AudioEncoder, err error) {
	var id uint32

	switch typ {
	case av.AAC:
		id = C.AV_CODEC_ID_AAC

	default:
		err = fmt.Errorf("cannot find encoder codecType=%d", typ)
		return
	}

	codec := C.avcodec_find_encoder(id)
	if codec == nil {
		err = fmt.Errorf("cannot find encoder codecId=%d", id)
		return
	}

	if C.avcodec_get_type(id) != C.AVMEDIA_TYPE_AUDIO {
		err = fmt.Errorf("codecId=%d is not audio", id)
		return
	}

	_enc := &AudioEncoder{}
	if _enc.ff, err = newFFCtxByCodec(codec); err != nil {
		return
	}
	enc = _enc
	return
}

func NewAudioEncoderByName(name string) (enc *AudioEncoder, err error) {
	_enc := &AudioEncoder{}

	codec := C.avcodec_find_encoder_by_name(C.CString(name))
	if codec == nil {
		err = fmt.Errorf("cannot find encoder=%s", name)
		return
	}
	if C.avcodec_get_type(codec.id) != C.AVMEDIA_TYPE_AUDIO {
		err = fmt.Errorf("encoder=%s type is not audio", name)
		return
	}

	if _enc.ff, err = newFFCtxByCodec(codec); err != nil {
		return
	}
	enc = _enc
	return
}

func NewAudioDecoder(codec av.AudioCodecData) (dec *AudioDecoder, err error) {
	_dec := &AudioDecoder{}
	var id uint32

	switch codec.Type() {
	case av.AAC:
		if aaccodec, ok := codec.(av.AACCodecData); ok {
			_dec.Extradata = aaccodec.MPEG4AudioConfigBytes()
			id = C.AV_CODEC_ID_AAC
		} else {
			err = fmt.Errorf("aac CodecData must be av.AACCodecData")
			return
		}

	case av.PCM_MULAW:
		id = C.AV_CODEC_ID_PCM_MULAW

	default:
		if ffcodec, ok := codec.(AudioCodecData); ok {
			_dec.Extradata = ffcodec.extradata
			id = ffcodec.codecId
		} else {
			err = fmt.Errorf("invalid CodecData for ffmpeg to decode")
			return
		}
	}

	c := C.avcodec_find_decoder(id)
	if c == nil {
		err = fmt.Errorf("cannot find decoder id=%d", id)
		return
	}

	if C.avcodec_get_type(c.id) != C.AVMEDIA_TYPE_AUDIO {
		err = fmt.Errorf("decoder id=%d type is not audio", c.id)
		return
	}

	if _dec.ff, err = newFFCtxByCodec(c); err != nil {
		return
	}

	_dec.SampleFormat = codec.SampleFormat()
	_dec.SampleRate = codec.SampleRate()
	_dec.ChannelLayout = codec.ChannelLayout()
	if err = _dec.Setup(); err != nil {
		return
	}

	dec = _dec
	return
}

type AudioCodecData struct {
	codecId uint32
	sampleFormat av.SampleFormat
	channelLayout av.ChannelLayout
	sampleRate int
	extradata []byte
}

func (self AudioCodecData) Type() int {
	return int(self.codecId)
}

func (self AudioCodecData) IsAudio() bool {
	return true
}

func (self AudioCodecData) IsVideo() bool {
	return false
}

func (self AudioCodecData) SampleRate() int {
	return self.sampleRate
}

func (self AudioCodecData) SampleFormat() av.SampleFormat {
	return self.sampleFormat
}

func (self AudioCodecData) ChannelLayout() av.ChannelLayout {
	return self.channelLayout
}

