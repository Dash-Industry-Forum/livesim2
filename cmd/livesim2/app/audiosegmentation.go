package app

import (
	"fmt"
	"io/fs"
	"path"

	"github.com/Eyevinn/mp4ff/bits"
	"github.com/Eyevinn/mp4ff/mp4"
)

// audioRecipe represents the output values, and the intervals of input samples
type audioRecipe struct {
	rep                 *RepData
	segNr               uint32
	startTime           uint64
	endTime             uint64
	audioInStart        uint64
	audioInEnd          uint64
	audioInEndAfterWrap uint64
}

// calcAudioSegRecipe returns audioRecipe for a segment given a reference segment and representation data.
func calcAudioSegRecipe(refNr uint32, refStart, refEnd, refTotalDur, refTimescale uint64, rd *RepData) audioRecipe {
	sampleDur := uint64(*rd.ConstantSampleDuration)
	audioStart := calcAudioTimeFromRef(refStart, refTimescale, sampleDur, uint64(rd.MediaTimescale))
	audioEnd := calcAudioTimeFromRef(refEnd, refTimescale, sampleDur, uint64(rd.MediaTimescale))
	startWrap := refStart / refTotalDur * refTotalDur
	endWrap := refEnd / refTotalDur * refTotalDur
	audioWrapStart := calcAudioTimeFromRef(startWrap, refTimescale, sampleDur, uint64(rd.MediaTimescale))
	audioWrapEnd := calcAudioTimeFromRef(endWrap, refTimescale, sampleDur, uint64(rd.MediaTimescale))
	audioInStart := audioStart - audioWrapStart
	if audioWrapEnd > audioWrapStart {
		if audioEnd < audioWrapEnd+sampleDur {
			audioInEnd := audioEnd - audioWrapStart
			return audioRecipe{rd, refNr, audioStart, audioEnd, audioInStart, audioInEnd, 0}
		} else {
			audioInEnd := audioWrapEnd - audioWrapStart
			audioInEndAfterWrap := audioEnd - audioWrapEnd
			return audioRecipe{rd, refNr, audioStart, audioEnd, audioInStart, audioInEnd, audioInEndAfterWrap}
		}
	}
	return audioRecipe{rd, refNr, audioStart, audioEnd, audioInStart, audioInStart + (audioEnd - audioStart), 0}
}

// calcAudioTimeFromRef returns audioTime right at or within one frameDur from refTime.
// A frame is one mp4 sample, such as an AAC frame which is normally 1024 audio samples.
func calcAudioTimeFromRef(refTime, refTimescale, audioFrameDur, audioTimescale uint64) uint64 {
	audioOutTime := (refTime * audioTimescale / refTimescale) / audioFrameDur * audioFrameDur
	if audioOutTime*refTimescale < refTime*audioTimescale {
		audioOutTime += audioFrameDur
	}
	return audioOutTime
}

type sampleItvl struct {
	segIdx        int
	startIdx      uint32
	endIdx        uint32
	nrFillSamples uint32
}

func (s sampleItvl) dur(sampleDur uint64) uint64 {
	return uint64(s.endIdx-s.startIdx) * sampleDur
}

// createAudioSeg takes a recipe and creates an output audio segment with the right samples.
func createAudioSeg(vodFS fs.FS, a *asset, cfg *ResponseConfig, rec audioRecipe) (*mp4.MediaSegment, error) {
	rep := rec.rep
	sampleDur := uint64(*rep.ConstantSampleDuration)
	var startSampleIdx, endSampleIdx uint32
	nextAudioStart := rec.audioInStart
	timeCollected := uint64(0)
	var sampleItvls []sampleItvl
	lastIdx := len(rep.Segments) - 1
	// Find a segment start nr that is early enough for audioInStart
	startNr := int(rec.audioInStart) / rep.duration()
	for {
		if rep.Segments[startNr].StartTime > rec.audioInStart {
			startNr--
			continue
		}
		break
	}
	for i := startNr; i <= lastIdx; i++ {
		s := rep.Segments[i]
		if s.EndTime <= rec.audioInStart {
			continue
		}
		if nextAudioStart < s.EndTime && len(sampleItvls) == 0 {
			startSampleIdx = uint32((nextAudioStart - s.StartTime) / sampleDur)
			itvl := sampleItvl{i, startSampleIdx, 0, 0}
			sampleItvls = append(sampleItvls, itvl)
		}
		if rec.audioInEnd >= s.EndTime {
			endSampleIdx = uint32((s.EndTime - s.StartTime) / sampleDur)
			sampleItvls[len(sampleItvls)-1].endIdx = endSampleIdx
			timeCollected += sampleItvls[len(sampleItvls)-1].dur(sampleDur)
			nextAudioStart = s.EndTime
			if nextAudioStart == rec.audioInEnd {
				break
			}
			if i < lastIdx {
				sampleItvls = append(sampleItvls, sampleItvl{i + 1, 0, 0, 0})
				continue
			}
			// Last segment and some time to wrap missing.
			// We need to fill by repeating some audio samples
			fillTime := (rec.audioInEnd - s.EndTime)
			timeCollected += fillTime
			nrFills := uint32(fillTime / sampleDur)
			sampleItvls[len(sampleItvls)-1].nrFillSamples = nrFills
			break
		}
		sampleItvls[len(sampleItvls)-1].endIdx = uint32((rec.audioInEnd - nextAudioStart) / sampleDur)
		timeCollected += sampleItvls[len(sampleItvls)-1].dur(sampleDur)
		break
	}
	audioLeft := (rec.endTime - rec.startTime - timeCollected)
	if audioLeft != rec.audioInEndAfterWrap {
		return nil, fmt.Errorf("audioLeft %d != audioInEndAfterWrap %d", audioLeft, rec.audioInEndAfterWrap)
	}
	if rec.audioInEndAfterWrap > 0 {
		sampleItvls = append(sampleItvls, sampleItvl{0, 0, 0, 0})
		for i, s := range rep.Segments {
			if rec.audioInEndAfterWrap < s.EndTime {
				sampleItvls[len(sampleItvls)-1].endIdx = uint32((rec.audioInEndAfterWrap - s.StartTime) / sampleDur)
				break
			}
			sampleItvls[len(sampleItvls)-1].endIdx = uint32((s.EndTime - s.StartTime) / sampleDur)
			sampleItvls = append(sampleItvls, sampleItvl{i + 1, 0, 0, 0})
		}
	}
	initSeg := rep.initSeg
	trex := getTrex(initSeg)
	nrOutSamples := (rec.endTime - rec.startTime) / sampleDur
	outputFullSamples := make([]mp4.FullSample, 0, nrOutSamples)

	var seg *mp4.MediaSegment

	for _, itvl := range sampleItvls {
		s := rep.Segments[itvl.segIdx]
		segPath := path.Join(a.AssetPath, replaceTimeAndNr(rep.MediaURI, s.StartTime, s.Nr))
		data, err := fs.ReadFile(vodFS, segPath)
		if err != nil {
			return nil, fmt.Errorf("read segment: %w", err)
		}
		sr := bits.NewFixedSliceReader(data)
		fSeg, err := mp4.DecodeFileSR(sr)
		if err != nil {
			return nil, fmt.Errorf("decode segment: %w", err)
		}
		if len(fSeg.Segments) != 1 {
			return nil, fmt.Errorf("file has %d segments, expected 1", len(rep.Segments))
		}
		seg = fSeg.Segments[0]
		fss := make([]mp4.FullSample, 0, (s.dur() / sampleDur))
		for _, frag := range seg.Fragments {
			fs, err := frag.GetFullSamples(trex)
			if err != nil {
				return nil, fmt.Errorf("getFullSamples: %w", err)
			}
			fss = append(fss, fs...)
		}
		outputFullSamples = append(outputFullSamples, fss[itvl.startIdx:itvl.endIdx]...)
		if itvl.nrFillSamples > 0 { // Repeat last sample to fill up
			for i := uint32(0); i < itvl.nrFillSamples; i++ {
				outputFullSamples = append(outputFullSamples, fss[len(fss)-1])
			}
		}
	}
	resetSegmentToNewSamples(seg,
		outputFullSamples,
		rec.segNr,
		rec.startTime)
	return seg, nil
}

func getTrex(initSeg *mp4.InitSegment) *mp4.TrexBox {
	if initSeg != nil && initSeg.Moov != nil && initSeg.Moov.Mvex != nil && initSeg.Moov.Mvex.Trex != nil {
		return initSeg.Moov.Mvex.Trex
	}
	return nil
}

// resetSegmentToNewSamples sets the segment to use one fragment with full samples from fss.
func resetSegmentToNewSamples(seg *mp4.MediaSegment, fss []mp4.FullSample, seqNr uint32, baseMediaDecodeTime uint64) {
	seg.Fragments = seg.Fragments[:1]
	nrNewSamples := len(fss)
	frag := seg.Fragments[0]
	trun := frag.Moof.Traf.Trun
	trun.Samples = make([]mp4.Sample, 0, nrNewSamples)
	totDataSize := uint32(0)
	for i := range fss {
		totDataSize += fss[i].Size
	}
	frag.Mdat.Data = make([]byte, 0, totDataSize)
	for i := range fss {
		frag.AddFullSample(fss[i])
	}
	frag.Moof.Mfhd.SequenceNumber = seqNr
	frag.Moof.Traf.Tfdt.SetBaseMediaDecodeTime(baseMediaDecodeTime)
	// _  = frag.Moof.Traf.OptimizeTfhdTrun()
}
