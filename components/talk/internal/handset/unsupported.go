//go:build ((!darwin && !linux) || (darwin && !cgo)) && !talk_test_audio

package handset

import "context"

func (l *Listener) run(ctx context.Context) {
	l.update(false, "USB handset buttons require macOS or Linux; use talk call and talk volume")
	<-ctx.Done()
}
