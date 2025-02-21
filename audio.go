package spx

import (
	"io"
	"sync"

	"github.com/qiniu/audio/convert"
	_ "github.com/qiniu/audio/mp3"       // support mp3
	_ "github.com/qiniu/audio/wav"       // support wav/pcm
	_ "github.com/qiniu/audio/wav/adpcm" // support wav/adpcm

	"github.com/hajimehoshi/ebiten/v2/audio"

	qaudio "github.com/qiniu/audio"
)

// -------------------------------------------------------------------------------------

type readSeekCloser struct {
	io.ReadCloser
}

type readCloser struct {
	io.ReadSeeker
	io.Closer
}

func (p *readSeekCloser) Seek(offset int64, whence int) (int64, error) {
	panic("can't seek")
}

func newReadSeeker(source io.ReadCloser) io.ReadSeeker {
	if r, ok := source.(io.ReadSeeker); ok {
		return r
	}
	return &readSeekCloser{source}
}

// -------------------------------------------------------------------------------------

type playerState int

const (
	playerPaused playerState = iota
	playerPlay
	playerLoopPlay
	playerClosed
)

type ActionState int

const (
	ActionPlay ActionState = iota
	ActionLoopPlay
	ActionLoopContinuePlay
	ActionPause
	ActionResume
	ActionStop
)

type PlayOptions struct {
	Wait   bool        `json:"wait"`
	Action ActionState `json:"action"`
}

type soundPlayer struct {
	*audio.Player
	media Sound
	state playerState
}
type soundMgr struct {
	g            *Game
	audioContext *audio.Context
	players      map[*soundPlayer]chan bool
	playersM     sync.Mutex
}

const (
	defaultSampleRate = 44100
	defaultRatio      = 100.0
)

func (p *soundMgr) addPlayer(sp *soundPlayer, done chan bool) {
	p.playersM.Lock()
	defer p.playersM.Unlock()

	p.players[sp] = done
}

func (p *soundMgr) init(g *Game) {
	audioContext := audio.NewContext(defaultSampleRate)
	p.audioContext = audioContext
	p.players = make(map[*soundPlayer]chan bool)
	p.g = g
}

func (p *soundMgr) update() {
	p.playersM.Lock()
	defer p.playersM.Unlock()

	var closed []*soundPlayer
	for sp, done := range p.players {
		if !sp.IsPlaying() && sp.state != playerPaused {
			if sp.state == playerLoopPlay {
				sp.Rewind()
				sp.Play()
				continue
			}
			sp.Close()
			if done != nil {
				done <- true
			}
			closed = append(closed, sp)
		}
	}
	for _, sp := range closed {
		delete(p.players, sp)
	}
}

func (p *soundMgr) stopAll() {
	p.playersM.Lock()
	defer p.playersM.Unlock()

	closed := make([]*soundPlayer, 0, len(p.players))
	for sp, done := range p.players {
		sp.Close()
		if done != nil {
			done <- true
		}
		sp.state = playerClosed
		closed = append(closed, sp)
	}
	for _, sp := range closed {
		delete(p.players, sp)
	}
}

func (p *soundMgr) playAction(media Sound, actionPlay *PlayOptions) (err error) {

	switch actionPlay.Action {
	case ActionPlay:
		err = p.play(media, actionPlay.Wait, ActionPlay)
	case ActionLoopPlay:
		err = p.play(media, actionPlay.Wait, ActionLoopPlay)
	case ActionLoopContinuePlay:
		err = p.playContinue(media, actionPlay.Wait)
	case ActionStop:
		p.stop(media)
	case ActionResume:
		p.resume(media)
	case ActionPause:
		p.pause(media)
	}
	return
}
func (p *soundMgr) playContinue(media Sound, wait bool) (err error) {
	p.playersM.Lock()
	isFound := false
	for sp, _ := range p.players {
		if sp.media.Path == media.Path {
			sp.state = playerLoopPlay
			isFound = true

		}
	}
	p.playersM.Unlock()

	if isFound == false {
		err = p.play(media, wait, ActionLoopPlay)
	}
	return
}
func (p *soundMgr) play(media Sound, wait bool, action ActionState) (err error) {

	source, err := p.g.fs.Open(media.Path)
	if err != nil {
		panic(err)
	}

	audioContext := p.audioContext
	d, _, err := qaudio.Decode(newReadSeeker(source))
	if err != nil {
		source.Close()
		return
	}

	d = convert.ToStereo16(d)
	d = convert.Resample(d, audioContext.SampleRate())

	sp := &soundPlayer{}
	sp.media = media
	sp.Player, err = audioContext.NewPlayer(&readCloser{d, source})
	if err != nil {
		source.Close()
		return
	}

	var done chan bool
	if wait {
		done = make(chan bool, 1)
	}
	p.addPlayer(sp, done)
	sp.Play()
	switch action {
	case ActionPlay:
		sp.state = playerPlay
	case ActionLoopPlay:
		sp.state = playerLoopPlay
	}

	if wait {
		waitForChan(done)
	}
	return
}

func (p *soundMgr) stop(media Sound) {
	p.playersM.Lock()
	defer p.playersM.Unlock()

	closed := make([]*soundPlayer, 0, len(p.players))
	for sp, done := range p.players {
		if sp.media.Path == media.Path {
			sp.Close()
			if done != nil {
				done <- true
			}
			sp.state = playerClosed
			closed = append(closed, sp)
		}
	}
	for _, sp := range closed {
		delete(p.players, sp)
	}
}

func (p *soundMgr) pause(media Sound) {
	p.playersM.Lock()
	defer p.playersM.Unlock()

	for sp, _ := range p.players {
		if sp.media.Path == media.Path {
			sp.Pause()
			sp.state = playerPaused

		}

	}
}

func (p *soundMgr) resume(media Sound) {
	p.playersM.Lock()
	defer p.playersM.Unlock()
	for sp, _ := range p.players {
		if sp.media.Path == media.Path {
			sp.Play()
			sp.state = playerPlay

		}

	}
}

func (p *soundMgr) volume() float64 {
	for sp := range p.players {
		return sp.Volume() * defaultRatio
	}
	return 0
}

func (p *soundMgr) SetVolume(volume float64) {
	for sp := range p.players {
		sp.SetVolume(volume / defaultRatio)
	}
}

func (p *soundMgr) ChangeVolume(delta float64) {
	v := p.volume()
	for sp := range p.players {
		sp.SetVolume((v + delta) / defaultRatio)
	}
}

// -------------------------------------------------------------------------------------
