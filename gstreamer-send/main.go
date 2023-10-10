package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	gst "github.com/pion/example-webrtc-applications/v3/internal/gstreamer-src"
	"github.com/pion/example-webrtc-applications/v3/internal/signal"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"
)

func main() {
	audioSrc := flag.String("audio-src", "audiotestsrc samplesperbuffer=320 tick-interval=1000000000 wave=ticks", "GStreamer audio src")
	videoSrc := flag.String("video-src", "videotestsrc horizontal-speed=20 is-live=TRUE ! video/x-raw,width=1280,height=720,framerate=30/1 ! timeoverlay ", "GStreamer video src")
	flag.Parse()

	// Everything below is the pion-WebRTC API! Thanks for using it ❤️.

	// Prepare the configuration
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	enableSR := false
	var peerConnection *webrtc.PeerConnection
	var err error

	if enableSR {
		// Create a new RTCPeerConnection
		peerConnection, err = webrtc.NewPeerConnection(config)
		if err != nil {
			panic(err)
		}

	} else {
		mediaEngine := webrtc.MediaEngine{}
		if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
			panic(err)
		}
		api := webrtc.NewAPI(webrtc.WithMediaEngine(&mediaEngine))
		peerConnection, err = api.NewPeerConnection(config)
		if err != nil {
			panic(err)
		}
	}

	// Create a audio track
	//audioTrack, err := webrtc.NewTrackLocalStaticRTP(
	//	webrtc.RTPCodecCapability{MimeType: "audio/pcmu"}, "audio", "pion1")
	audioTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "audio/pcmu"}, "audio", "pion1")
	if err != nil {
		panic(err)
	}
	audioRTPSender, err := peerConnection.AddTrack(audioTrack)
	if err != nil {
		panic(err)
	}
	processRTCP(audioRTPSender)

	// Create a video track
	//firstVideoTrack, err := webrtc.NewTrackLocalStaticRTP(
	//	webrtc.RTPCodecCapability{MimeType: "video/vp8"}, "video", "pion2")
	/*
		firstVideoTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "video/vp8"}, "video", "pion2")
		//firstVideoTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "video/vp8"}, "video", "pion2")
		if err != nil {
			panic(err)
		}
		videoRTPSender, err := peerConnection.AddTrack(firstVideoTrack)
		if err != nil {
			panic(err)
		}
	*/

	// Create a second video track

	secondVideoTrack, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: "video/h264"}, "video", "pion3")
	if err != nil {
		panic(err)
	}
	secondVideoRTPSender, err := peerConnection.AddTrack(secondVideoTrack)
	if err != nil {
		panic(err)
	}

	processRTCP(secondVideoRTPSender)

	// Set the handler for ICE connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		fmt.Printf("Connection State has changed %s \n", connectionState.String())
		if connectionState.String() == "disconnected" {
			//audioRTPSender.Stop()
			//secondVideoRTPSender.Stop()
		}
	})

	/*
		go func(rtpSender *webrtc.RTPSender) {
			for range time.NewTicker(time.Second * 2).C {

				params := rtpSender.GetParameters()
				rtpSender.Track().StreamID()

				var ssrc uint32

				for _, encoding := range params.Encodings {
					ssrc = uint32(encoding.SSRC)

					fmt.Printf("StreamID: %s, Kind %s, RID: %s, SSRC: %x, PT: %d\n",
						rtpSender.Track().StreamID(),
						rtpSender.Track().Kind(),
						encoding.RID, ssrc, encoding.PayloadType)
				}

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
				peerConnection.WriteRTCP(packets)

			}
		}(audioRTPSender)
	*/

	// Wait for the offer to be pasted
	offer := webrtc.SessionDescription{}
	signal.Decode(signal.MustReadStdin(), &offer)

	// Set the remote SessionDescription
	err = peerConnection.SetRemoteDescription(offer)
	if err != nil {
		panic(err)
	}

	// Create an answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		panic(err)
	}

	// Create channel that is blocked until ICE Gathering is complete
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	// Sets the LocalDescription, and starts our UDP listeners
	err = peerConnection.SetLocalDescription(answer)
	if err != nil {
		panic(err)
	}

	<-gatherComplete

	// Output the answer in base64 so we can paste it in browser
	fmt.Println(signal.Encode(*peerConnection.LocalDescription()))

	// Start pushing buffers on these tracks
	gst.CreatePipeline("pcmu", []*webrtc.TrackLocalStaticSample{audioTrack}, []*webrtc.RTPSender{audioRTPSender}, *audioSrc).Start()
	//gst.CreatePipeline("vp8", []*webrtc.TrackLocalStaticSample{firstVideoTrack}, []*webrtc.RTPSender{videoRTPSender}, *videoSrc).Start()
	gst.CreatePipeline("h264", []*webrtc.TrackLocalStaticSample{secondVideoTrack}, []*webrtc.RTPSender{secondVideoRTPSender}, *videoSrc).Start()
	// Block forever
	select {}
}

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

func processRTCP(rtpSender *webrtc.RTPSender) {
	go func() {
		rtcpBuf := make([]byte, 1500)

		for {
			log.Printf("read\n")
			n, _, rtcpErr := rtpSender.Read(rtcpBuf)
			log.Printf("read done\n")
			if rtcpErr != nil {
				log.Printf("exit\n")
				return
			}
			//log.Printf("RTCP packet received: %s", hex.Dump(rtcpBuf[:n]))

			packets, err := rtcp.Unmarshal(rtcpBuf[:n])
			if err != nil {
				log.Print(err)
				return
			}
			for _, packet := range packets {
				log.Printf("RTCP packet received: %T Kind: %s", packet, rtpSender.Track().Kind())
				switch rtcpPacket := packet.(type) {
				case *rtcp.ReceiverReport:
					log.Printf("RR SSRC from %x: ", rtcpPacket.SSRC)

					for _, report := range rtcpPacket.Reports {
						s := uint16(report.LastSenderReport >> 16)
						fractionalPart := report.LastSenderReport & 0xFFFF

						log.Printf("RR SSRC %x Lost: %d, TotalLost: %d, lastSEQENCE: %d, jitter: %d, lastSSNTP second: %d.%d, DLSR: %d\n",
							report.SSRC,
							report.FractionLost,
							report.TotalLost,
							report.LastSequenceNumber, report.Jitter, s, fractionalPart, report.Delay)
					}
					break
				case *rtcp.ReceiverEstimatedMaximumBitrate:
					log.Printf("Estimated Bitrate: %f", rtcpPacket.Bitrate)
					break
				default:

					log.Printf("RTCP packet received: %T", rtcpPacket)
				}
			}
		}
		log.Printf("exit processRTCP\n")
	}()
}
