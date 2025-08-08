package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ebitengine/oto/v3"
	"github.com/hajimehoshi/go-mp3"
)

// Console colors - globals
var Reset = "\033[0m"
var Red = "\033[31m"
var Green = "\033[32m"
var Yellow = "\033[33m"
var Blue = "\033[34m"
var Magenta = "\033[35m"
var Cyan = "\033[36m"
var Gray = "\033[37m"
var White = "\033[97m"

// Globals
var channels map[string]bool = make(map[string]bool)
var otoCtx *oto.Context

type ChannelStatus struct {
	Name        string
	IsLive      bool
	HasPlayed   bool
	LastChanged time.Time
	PlayTTS     bool
}

// util :)
func check(e error) {
	if e != nil {
		panic(e)
	}
}

// Get checksum from the file
func getFileChecksum(filePath string) []byte {
	f, err := os.Open(filePath)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("checksum: %x\n", h.Sum(nil))
	return h.Sum(nil)
}

// Read config file and parse channel=true/false format
func getChannelsFromConfig(filePath string) map[string]bool {
	channels := make(map[string]bool)
	file, err := os.Open(filePath)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue // Skip empty lines and comments
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			fmt.Printf("Skipping invalid line: %s\n", line)
			continue
		}

		channel := strings.TrimSpace(parts[0])
		ttsEnabled := strings.TrimSpace(strings.ToLower(parts[1])) == "true"

		channels[channel] = ttsEnabled
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
	return channels
}

func initOto() (*oto.Context, error) {
	// Prepare an Oto context (this will use your default audio device) that will
	// play all our sounds. Its configuration can't be changed later.
	op := &oto.NewContextOptions{
		SampleRate:   44100,
		ChannelCount: 1, // Mono output
		Format:       oto.FormatSignedInt16LE,
	}

	// Create Oto context
	otoCtx, _, err := oto.NewContext(op)
	if err != nil {
		return nil, fmt.Errorf("oto.NewContext failed: %w", err)
	}

	return otoCtx, nil
}

// Check if mp3 file for channel exists
func checkMp3(channel string) bool {
	if _, err := os.Stat("mp3/" + channel + ".mp3"); err == nil {
		return true
	}
	return false
}

func playMp3(file []byte, volume float64) {
	// Check if file data is valid
	if len(file) == 0 {
		fmt.Println("Warning: Empty MP3 data, skipping playback")
		return
	}

	fileBytesReader := bytes.NewReader(file)
	decodedMp3, err := mp3.NewDecoder(fileBytesReader)
	if err != nil {
		fmt.Printf("Warning: mp3.NewDecoder failed: %s, skipping playback\n", err.Error())
		return
	}
	player := otoCtx.NewPlayer(decodedMp3)

	player.SetVolume(volume)

	player.Play()

	for player.IsPlaying() {
		time.Sleep(time.Millisecond)
	}

	err = player.Close()
	if err != nil {
		fmt.Printf("Warning: player.Close failed: %s\n", err.Error())
	}
}

// Check the stream status using the CDN
func checkStreamStatus(channel string) (string, bool) {
	timestamp := time.Now().Unix()
	url := fmt.Sprintf("https://static-cdn.jtvnw.net/previews-ttv/live_user_%s-320x180.jpg?timestamp=%d", channel, timestamp)

	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("Error fetching the URL: %v\n", err)
		return channel, false
	}
	defer resp.Body.Close()

	finalURL := resp.Request.URL.String()
	if strings.Contains(finalURL, "404_preview") {
		return channel, false
	} else {
		return channel, true
	}
}

// Check if mp3 file exists, otherwise generate one
// TODO: add local TTS
func getMp3ForChannel(channel string) []byte {
	var body []byte
	haveFile := checkMp3(channel)
	if haveFile {
		body, err := os.ReadFile("mp3/" + channel + ".mp3")
		if err != nil {
			fmt.Printf("Error reading file: %v\n", err)
			return nil
		}
		return body
	} else {
		textParam := url.QueryEscape(channel + " is now live.")
		streamElementsUrl := fmt.Sprintf("https://api.streamelements.com/kappa/v2/speech?voice=Brian&text=%s", textParam)

		resp, err := http.Get(streamElementsUrl)
		if err != nil {
			fmt.Printf("Error fetching the URL: %v\n", err)
			return nil
		}
		defer resp.Body.Close()

		time.Sleep(500 * time.Millisecond)

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotModified {
			fmt.Printf("Failed to fetch the URL. HTTP Status: %s\n%s\n", resp.Status, streamElementsUrl)
			return nil
		}

		body, err = io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("Error reading response body: %v\n", err)
			return nil
		}
		err = os.WriteFile("mp3/"+channel+".mp3", body, 0644)
		check(err)
		fmt.Printf("Wrote %s.mp3\n", channel)
		return body
	}
}

// Initialize the slice of channel statuses
func initStreamsSlice() []ChannelStatus {
	var channelStatuses []ChannelStatus
	for channel, playTTS := range channels {
		_, status := checkStreamStatus(channel)
		channelStatuses = append(channelStatuses, ChannelStatus{
			Name:        channel,
			IsLive:      status,
			HasPlayed:   false,
			LastChanged: time.Now(),
			PlayTTS:     playTTS,
		})
	}
	return channelStatuses
}

// Print the slice of channel statuses
func printSlice(channels []ChannelStatus) {
	for _, channel := range channels {
		ttsIndicator := ""
		if channel.PlayTTS {
			ttsIndicator = fmt.Sprintf(" %s[TTS]%s", Yellow, Reset)
		}

		if channel.IsLive { // If live
			fmt.Printf("%-17s -> %s[ %s ]%s%s hasPlayed: %s%t%s\n",
				channel.Name, Green, "LIVE", Reset, ttsIndicator, Cyan, channel.HasPlayed, Reset)
		} else { // If offline
			continue
			fmt.Printf("%-17s -> %s[ %s ]%s%s hasPlayed: %s%t%s\n",
				channel.Name, Red, "OFFLINE", Reset, ttsIndicator, Cyan, channel.HasPlayed, Reset)
		}
	}
}

func main() {
	configFile := "channels.txt"
	channels = getChannelsFromConfig(configFile)
	otoCtx, _ = initOto()

	fmt.Println("Loaded channels:")
	for channel, playTTS := range channels {
		ttsStatus := "[TTS OFF]"
		if playTTS {
			ttsStatus = "[TTS ON]"
		}
		fmt.Printf("  %s = %s\n", channel, ttsStatus)
	}

	fmt.Println("Pre-generating TTS files for all channels...")
	time.Sleep(2 * time.Second)

	// Pre-gen TTS for ALL Channels
	for channel := range channels {
		fmt.Printf("Checking TTS for %s\n", channel)
		getMp3ForChannel(channel)

	}

	time.Sleep(4 * time.Second)
	channelStatuses := initStreamsSlice()
	printSlice(channelStatuses)

	// Enter the main monitoring loop
	for {
		for i := range channelStatuses {
			_, isOnline := checkStreamStatus(channelStatuses[i].Name)
			time.Sleep(time.Millisecond * 100)
			currentTime := time.Now()

			if channelStatuses[i].IsLive && !channelStatuses[i].HasPlayed {
				// If live but hasnt played
				if channelStatuses[i].PlayTTS {
					mp3 := getMp3ForChannel(channelStatuses[i].Name)
					if len(mp3) > 0 {
						fmt.Printf("Playing mp3 from local file %s.mp3\n", channelStatuses[i].Name)
						playMp3(mp3, 0.10)
						channelStatuses[i].HasPlayed = true
					}
				}
			} else if channelStatuses[i].IsLive == isOnline {
				// If status hasnt changed, move on
				continue
			} else if !channelStatuses[i].IsLive && isOnline {
				// If previously offline but now online
				if currentTime.Sub(channelStatuses[i].LastChanged) > 5*time.Minute {
					// If enough time passed (+5 minutes)
					if channelStatuses[i].PlayTTS {
						mp3 := getMp3ForChannel(channelStatuses[i].Name)
						if len(mp3) > 0 {
							fmt.Printf("Playing mp3 from local file %s.mp3\n", channelStatuses[i].Name)
							playMp3(mp3, 0.10)
							channelStatuses[i].HasPlayed = true
						}
					}
					channelStatuses[i].LastChanged = currentTime
				}
				channelStatuses[i].IsLive = true
			} else if channelStatuses[i].IsLive && !isOnline {
				// If previously live but now offline
				channelStatuses[i].IsLive = false
				channelStatuses[i].HasPlayed = false
				channelStatuses[i].LastChanged = currentTime
			}
		}

		// clear screen 0_o
		fmt.Print("\033[2J\033[H")

		printSlice(channelStatuses)
		fmt.Println("Waiting 5 minutes...")
		time.Sleep(5 * time.Minute)
	}
}
