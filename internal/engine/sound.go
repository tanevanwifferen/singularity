package engine

import (
	"fmt"
	"os/exec"

	"gitlab.com/tanevanwifferen1/singularity/internal/config"
)

// playSound plays a notification sound based on the SoundConfig.
// If File is empty it falls back to the terminal bell character.
// Audio file playback tries common Linux players in order.
func playSound(cfg config.SoundConfig) {
	if !cfg.Enabled {
		return
	}
	if cfg.File == "" {
		fmt.Print("\a")
		return
	}
	for _, player := range []string{"paplay", "pw-play", "aplay", "mpv", "ffplay", "afplay"} {
		if path, err := exec.LookPath(player); err == nil {
			cmd := exec.Command(path, cfg.File)
			_ = cmd.Start() // fire-and-forget
			return
		}
	}
	// No player found — fall back to bell
	fmt.Print("\a")
}
