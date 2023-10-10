// Package gst provides an easy API to create an appsink pipeline
package gst

/*
#cgo pkg-config: gstreamer-1.0 gstreamer-app-1.0

#include "gst.h"

*/
import "C"

import (
	"container/list"
	"math"
	"math/bits"
	"sync"
	"time"
	"unsafe"

	"github.com/pion/logging"
	"github.com/pion/randutil"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

func init() {
	go C.gstreamer_send_start_mainloop()
}

type senderStream struct {
	clockRate float64
	m         sync.Mutex

	// data from rtp packets
	lastRTPTimeRTP  uint32
	lastRTPTimeTime time.Time
	packetCount     uint32
	octetCount      uint32

	log logging.LeveledLogger
}

func newSenderStream(clockRate uint32) *senderStream {
	loggerFactory := logging.NewDefaultLoggerFactory()
	loggerFactory.DefaultLogLevel = logging.LogLevelInfo
	return &senderStream{
		clockRate: float64(clockRate),
		log:       loggerFactory.NewLogger("senderStream"),
	}
}

func (stream *senderStream) processRTP(now time.Time, rtptime uint32, packet_count int, payload_len uint32) {
	stream.m.Lock()
	defer stream.m.Unlock()

	/*
		stream.log.Infof("processRTP NTPTIme: %d, RTPTime: %d, PacketCount: %d, OctetCount: %d",
			now.Nanosecond(), rtptime, packet_count, payload_len)
	*/
	// always update time to minimize errors
	stream.lastRTPTimeRTP = rtptime
	stream.lastRTPTimeTime = now

	stream.packetCount += uint32(packet_count)
	stream.octetCount += payload_len
}

// Pipeline is a wrapper for a GStreamer Pipeline
type Pipeline struct {
	Pipeline *C.GstElement
	//tracks     []*webrtc.TrackLocalStaticRTP
	tracks     []*webrtc.TrackLocalStaticSample
	senders    []*webrtc.RTPSender
	packetizer rtp.Packetizer
	id         int
	codecName  string
	clockRate  float32

	stream            *senderStream
	mediaSampleChanel chan *media.Sample
}

var (
	pipelines     = make(map[int]*Pipeline)
	pipelinesLock sync.Mutex
)

const (
	videoClockRate = 90000
	audioClockRate = 48000
	pcmClockRate   = 8000
)

type MediaSample struct {
	sample media.Sample
}

// CreatePipeline creates a GStreamer Pipeline
func CreatePipeline(codecName string,
	tracks []*webrtc.TrackLocalStaticSample, senders []*webrtc.RTPSender, pipelineSrc string) *Pipeline {
	//tracks []*webrtc.TrackLocalStaticRTP, senders []*webrtc.RTPSender, pipelineSrc string) *Pipeline {
	pipelineStr := "appsink name=appsink"
	var clockRate float32
	randomGenerator := randutil.NewMathRandomGenerator()
	var packetizer rtp.Packetizer

	switch codecName {
	case "vp8":
		pipelineStr = pipelineSrc + " ! vp8enc error-resilient=partitions keyframe-max-dist=10 auto-alt-ref=true cpu-used=5 deadline=1 ! " + pipelineStr
		clockRate = videoClockRate

		payloader := rtp.Payloader(&codecs.VP8Payloader{})
		ssrc := uint32(randomGenerator.Intn(math.MaxUint32))
		packetizer = rtp.NewPacketizer(
			1200,
			0, // payload type
			ssrc,
			payloader,
			rtp.NewRandomSequencer(),
			90000,
		)

	case "vp9":
		pipelineStr = pipelineSrc + " ! vp9enc ! " + pipelineStr
		clockRate = videoClockRate

	case "h264":
		//pipelineStr = pipelineSrc + " ! video/x-raw,format=I420 ! x264enc speed-preset=ultrafast tune=zerolatency key-int-max=20 ! video/x-h264,stream-format=byte-stream ! queue max-size-buffers=0 max-size-time=0 max-size-bytes=0 min-threshold-time=3333333333 !" + pipelineStr
		//pipelineStr = pipelineSrc + " ! video/x-raw,format=I420 ! x264enc speed-preset=ultrafast tune=zerolatency key-int-max=20 ! video/x-h264,stream-format=byte-stream ! netsim drop-probability=0.5 !" + pipelineStr
		pipelineStr = pipelineSrc + " ! video/x-raw,format=I420 ! x264enc speed-preset=ultrafast tune=zerolatency key-int-max=20 bitrate=500 ! video/x-h264,stream-format=byte-stream ! " + pipelineStr
		clockRate = videoClockRate

	case "opus":
		pipelineStr = pipelineSrc + " ! opusenc ! " + pipelineStr
		clockRate = audioClockRate

	case "g722":
		pipelineStr = pipelineSrc + " ! avenc_g722 ! " + pipelineStr
		clockRate = audioClockRate

	case "pcmu":
		pipelineStr = pipelineSrc + " ! audio/x-raw, rate=8000 ! mulawenc ! " + pipelineStr
		clockRate = pcmClockRate

		payloader := rtp.Payloader(&codecs.G711Payloader{})
		ssrc := uint32(randomGenerator.Intn(math.MaxUint32))
		packetizer = rtp.NewPacketizer(
			1200,
			0, // payload type
			ssrc,
			payloader,
			rtp.NewRandomSequencer(),
			8000,
		)

	case "pcma":
		pipelineStr = pipelineSrc + " ! audio/x-raw, rate=8000 ! alawenc ! " + pipelineStr
		clockRate = pcmClockRate

	default:
		panic("Unhandled codec " + codecName)
	}

	pipelineStrUnsafe := C.CString(pipelineStr)
	defer C.free(unsafe.Pointer(pipelineStrUnsafe))

	pipelinesLock.Lock()
	defer pipelinesLock.Unlock()

	pipeline := &Pipeline{
		Pipeline:          C.gstreamer_send_create_pipeline(pipelineStrUnsafe),
		tracks:            tracks,
		senders:           senders,
		packetizer:        packetizer,
		id:                len(pipelines),
		codecName:         codecName,
		clockRate:         clockRate,
		stream:            newSenderStream(uint32(clockRate)),
		mediaSampleChanel: make(chan *media.Sample),
	}

	pipelines[pipeline.id] = pipeline
	return pipeline
}

/*
var (

	ntpEpoch = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)

)

type NtpTime uint64

	func ToNtpTime(t time.Time) NtpTime {
		nsec := uint64(t.Sub(ntpEpoch))
		sec := nsec / 1e9
		nsec = (nsec - sec*1e9) << 32
		frac := nsec / 1e9
		if nsec%1e9 >= 1e9/2 {
			frac++
		}
		return NtpTime(sec<<32 | frac)
	}

	func ntpTime(t time.Time) uint64 {
		// seconds since 1st January 1900
		s := (float64(t.UnixNano()) / 1000000000) + 2208988800

		// higher 32 bits are the integer part, lower 32 bits are the fractional part
		integerPart := uint32(s)
		fractionalPart := uint32((s - float64(integerPart)) * 0xFFFFFFFF)
		return uint64(integerPart)<<32 | uint64(fractionalPart)
	}

	func ntpTimeFromNano(nanoSecond int64) uint64 {
		// seconds since 1st January 1900
		s := (float64(nanoSecond) / 1000000000) + 2208988800

		// higher 32 bits are the integer part, lower 32 bits are the fractional part
		integerPart := uint32(s)
		fractionalPart := uint32((s - float64(integerPart)) * 0xFFFFFFFF)
		return uint64(integerPart)<<32 | uint64(fractionalPart)
	}
*/
const (
	ntpEpoch = 2208988800
)

var ntpEpochTime = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)

func NTPToTime(ntp uint64) time.Time {
	sec := uint32(ntp >> 32)
	frac := uint32(ntp & 0xFFFFFFFF)
	return ntpEpochTime.Add(
		time.Duration(sec)*time.Second +
			((time.Duration(frac) * time.Second) >> 32),
	)
}

func timeToNtp(ns int64) uint64 {
	seconds := uint64(ns/1e9 + ntpEpoch)
	fraction := uint64(((ns % 1e9) << 32) / 1e9)
	return seconds<<32 | fraction
}

func FromDuration(d time.Duration, hz uint32) int64 {
	if d < 0 {
		return -FromDuration(-d, hz)
	}
	hi, lo := bits.Mul64(uint64(d), uint64(hz))
	q, _ := bits.Div64(hi, lo, uint64(time.Second))
	return int64(q)
}

func writeSenderReport(stream *senderStream, ssrc uint32, rtpSender *webrtc.RTPSender, nanoSecondDelay int64, clockrate uint32) {
	stream.m.Lock()
	defer stream.m.Unlock()

	now := time.Now()
	nowNanoSecond := now.UnixNano()

	//nowNanoSecond += nanoSecondDelay
	//s := uint32((float64(nowNanoSecond) / 1000000000) + ntpEpoch)
	//seconds := uint64(nowNanoSecond/1e9 + ntpEpoch)
	//s := seconds & 0xFFFF

	var delayRTPTime uint32
	delayRTPTime = 0
	if nanoSecondDelay != 0 {
		delayRTPTime = uint32(FromDuration(time.Duration(nanoSecondDelay), uint32(stream.clockRate)))
	}

	diffNTPTIme := now.Sub(stream.lastRTPTimeTime).Nanoseconds()
	diffRTPTime := uint32(float64(diffNTPTIme*int64(stream.clockRate)) / 1000000000)

	nowRTP := stream.lastRTPTimeRTP + diffRTPTime + delayRTPTime
	ntpTime := timeToNtp(nowNanoSecond)

	sr_packet := &rtcp.SenderReport{
		SSRC:        ssrc,
		NTPTime:     ntpTime,
		RTPTime:     nowRTP,
		PacketCount: stream.packetCount,
		OctetCount:  stream.octetCount,
	}

	sde_packet := &rtcp.SourceDescription{
		Chunks: []rtcp.SourceDescriptionChunk{
			{
				Source: ssrc,
				Items: []rtcp.SourceDescriptionItem{
					rtcp.SourceDescriptionItem{
						Type: rtcp.SDESCNAME,
						Text: "0e9a398c-d34b-4dae-94c6-f9eb7c964fc1",
					},
				},
			},
		},
	}

	packets := []rtcp.Packet{sr_packet, sde_packet}

	/*
		fmt.Printf("SSRC: %x, NTP: %x second: %d s: %d, nowRTP: %d, plus RTPTime: %d, delayRTP: %d, PacketCount: %d, OctetCount: %d\n",
			ssrc, ntpTime, seconds, s, nowRTP, diffRTPTime, delayRTPTime, stream.packetCount, stream.octetCount)

		for _, r := range packets {
			// Print a string description of the packets
			if stringer, canString := r.(fmt.Stringer); canString {
				fmt.Printf("Send RTCP Packet: %v", stringer.String())
			}
		}
	*/

	//pkts := []rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(remote.SSRC())}}
	rtpSender.Transport().WriteRTCP(packets)
}

func SSWorker(stream *senderStream, rtpSender *webrtc.RTPSender) {
	for range time.NewTicker(time.Second * 1).C {

		params := rtpSender.GetParameters()
		rtpSender.Track().StreamID()

		for _, encoding := range params.Encodings {
			ssrc := uint32(encoding.SSRC)
			kind := rtpSender.Track().Kind()

			/*
				fmt.Printf("StreamID: %s, Kind %s, RID: %s, SSRC: %x, PT: %d\n",
					rtpSender.Track().StreamID(),
					kind,
					encoding.RID, ssrc, encoding.PayloadType)
			*/

			if kind == webrtc.RTPCodecTypeAudio {
				writeSenderReport(stream, ssrc, rtpSender, 0, pcmClockRate)
			} else if kind == webrtc.RTPCodecTypeVideo {
				writeSenderReport(stream, ssrc, rtpSender, 3333333333, videoClockRate)
			}
		}

		/*
			now := time.Now()
			nowNTP := ToNtpTime(now)

			sr_packet := &rtcp.SenderReport{
				SSRC:        ssrc,
				NTPTime:     uint64(nowNTP),
				RTPTime:     nowRTP,
				PacketCount: uint32(r.getTotalPacketsPrimary() + r.packetsDuplicate + r.packetsPadding),
				OctetCount:  uint32(r.bytes + r.bytesDuplicate + r.bytesPadding),
			}

			packets = []rtcp.Packet{sr_packet}

			//pkts := []rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(remote.SSRC())}}
			rtpSender.Transport().WriteRTCP(packets)
			//peerConnection.WriteRTCP(packets)
		*/
	}
}

// Start starts the GStreamer Pipeline
func (p *Pipeline) Start() {

	go SSWorker(p.stream, p.senders[0])

	if p.codecName == "h264" {
		go writeRTPRoutine(p, 0)
	} else {
		go writeRTPRoutine(p, 0)
	}

	C.gstreamer_send_start_pipeline(p.Pipeline, C.int(p.id))
}

// Stop stops the GStreamer Pipeline
func (p *Pipeline) Stop() {
	close(p.mediaSampleChanel)
	C.gstreamer_send_stop_pipeline(p.Pipeline)
}

func AudioPayloadLen2SampleCount(payload_len uint32, sample_size uint32) uint32 {
	sample_count := payload_len * 8 / sample_size
	return sample_count
}

func AudioPayloadSampleBytes2Time(payload_len uint32, sample_size uint32, clock_rate uint32) uint32 {
	sample_count := payload_len * 8 / sample_size
	val := 1000000000 / clock_rate
	time := sample_count * val
	return time
}

type MediaSampleQueue struct {
	mu             sync.Mutex
	totalDurations int64
	queue          list.List
}

func writeRTPRoutine(pipeline *Pipeline, nanoSecondDelay int64) {
	mediaSampeChannel := pipeline.mediaSampleChanel
	stream := pipeline.stream
	sampleChannel := make(chan *media.Sample)
	q := MediaSampleQueue{}

	go func(ch chan *media.Sample) {
		for {
			sample, ok := <-ch
			if !ok {
				break
			}

			for _, track := range pipeline.tracks {
				packet_count, err := track.WriteSample(*sample)
				if err != nil {
					panic(err)
				}

				rtptime := uint32(sample.Duration.Seconds() * stream.clockRate)
				now := time.Now()
				stream.processRTP(now, rtptime, packet_count, uint32(len(sample.Data)))
			}
		}
	}(sampleChannel)

	for {
		sample, ok := <-mediaSampeChannel
		if !ok {
			break
		}

		q.mu.Lock()

		// queue is full
		if q.totalDurations >= nanoSecondDelay {
			var firstSample *media.Sample

			// queue has element
			if q.queue.Len() > 0 {
				firstElement := q.queue.Front()
				firstSample = firstElement.Value.(*media.Sample)

				q.queue.Remove(firstElement)
				q.totalDurations -= firstSample.Duration.Nanoseconds()
				sampleChannel <- firstSample

				q.totalDurations += sample.Duration.Nanoseconds()
				q.queue.PushBack(sample)
			} else { // queue is empty
				//fmt.Printf("queue is empty\n")
				firstSample = sample
				sampleChannel <- firstSample
			}

		} else {
			q.queue.PushBack(sample)
			sampleDuration := sample.Duration.Nanoseconds()
			q.totalDurations += sampleDuration
		}

		q.mu.Unlock()

		/*
			rtptime := uint32(sample.Duration.Seconds() * stream.clockRate)

			for _, track := range pipeline.tracks {
				p := track.Packetizer()
				p.SkipSamples(rtptime)
			}
		*/
	}
	close(sampleChannel)
}

//export goHandlePipelineBuffer
func goHandlePipelineBuffer(buffer unsafe.Pointer, bufferLen C.int, duration C.int, pipelineID C.int) {
	pipelinesLock.Lock()
	pipeline, ok := pipelines[int(pipelineID)]
	pipelinesLock.Unlock()

	//stream := pipeline.stream
	mediaSampleChannel := pipeline.mediaSampleChanel

	//rtpPackets := packetizer.Packetize(pes.ElementaryStream, sample_count)

	if ok {

		data := C.GoBytes(buffer, bufferLen)
		duration_time := time.Duration(duration)

		//payload_len := uint32(len(data))

		/*
			payload_time := AudioPayloadSampleBytes2Time(payload_len, 8, 8000)
			fmt.Printf("code: %s, payload_len %d, time %d, duration %d, sample_count %d\n",
				pipeline.codecName,
				payload_len, payload_time, duration_time, sample_count)
		*/

		if pipeline.codecName == "vp8" {

			for _, track := range pipeline.tracks {
				sample := media.Sample{Data: data, Duration: time.Duration(duration_time)}
				if _, err := track.WriteSample(sample); err != nil {
					panic(err)
				}
				/*
					packetizer := pipeline.packetizer
					packets := packetizer.Packetize(data, sample_count)
					for _, p := range packets {
						track.WriteRTP(p)
					}
				*/
			}
		} else if pipeline.codecName == "pcmu" {
			sample := media.Sample{Data: data, Duration: time.Duration(duration_time)}
			mediaSampleChannel <- &sample

			/*
				sample_count := AudioPayloadLen2SampleCount(payload_len, 8)
				sample_count2 := uint32(sample.Duration.Seconds() * stream.clockRate)
				fmt.Printf("code: %s, payload_len %d, duration %d, sample_count %d %d\n",
					pipeline.codecName,
					payload_len, duration_time, sample_count, sample_count2)
			*/

			/*
				for _, track := range pipeline.tracks {
					sample := media.Sample{Data: data, Duration: time.Duration(duration_time)}
					mediaSampleChannel <- sample

					if err := track.WriteSample(sample); err != nil {
						panic(err)
					}

					now := time.Now()
					stream.processRTP(now, sample_count, payload_len)
				}
			*/

			/*
				packetizer := pipeline.packetizer
				for _, track := range pipeline.tracks {
					packets := packetizer.Packetize(data, sample_count)
					for _, p := range packets {
						track.rtpTrack.WriteRTP(p)
					}
				}
			*/
		} else if pipeline.codecName == "h264" {
			sample := media.Sample{Data: data, Duration: time.Duration(duration_time)}
			mediaSampleChannel <- &sample
			/*
				sample_count := uint32(sample.Duration.Seconds() * stream.clockRate)

				fmt.Printf("code: %s, payload_len %d, duration %d, sample_count %d\n",
					pipeline.codecName,
					payload_len, duration_time, sample_count)
			*/
			/*
				for _, track := range pipeline.tracks {
					if err := track.WriteSample(sample); err != nil {
						panic(err)
					}

					now := time.Now()
					rtptime := uint32(sample.Duration.Seconds() * stream.clockRate)
					stream.processRTP(now, rtptime, payload_len)
				}
			*/
			/*
				packetizer := pipeline.packetizer
				for _, track := range pipeline.tracks {
					packets := packetizer.Packetize(data, sample_count)
					for _, p := range packets {
						track.rtpTrack.WriteRTP(p)
					}
				}
			*/
		}
	}
	/*
		sample := media.Sample{Data: data, Duration: time.Duration(duration)}

		if ok {

			for _, t := range pipeline.tracks {

				if err := t.WriteSample(media.Sample{Data: C.GoBytes(buffer, bufferLen), Duration: time.Duration(duration)}); err != nil {
					panic(err)
				}

			}

			packets := packetizer.Packetize(datas, sample_count)
			for _, sender := range pipeline.senders {

				if err := t.WriteRTP(); err != nil {
					panic(err)
				}

			}
		} else {
			fmt.Printf("discarding buffer, no pipeline with id %d", int(pipelineID))
		}
	*/

	C.free(buffer)
}
