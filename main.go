package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
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
var channels []string = make([]string, 0, 16)
var otoCtx *oto.Context

type ChannelStatus struct {
	Name        string
	IsLive      bool
	HasPlayed   bool
	LastChanged time.Time
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

// Read each line from a file into a string slice
func getChannelsFromFile(filePath string) []string {
	tmp := make([]string, 0, 16)
	file, err := os.Open(filePath)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		tmp = append(tmp, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
	return tmp
}

func initOto() (*oto.Context, error) {
	// Prepare an Oto context (this will use your default audio device) that will
	// play all our sounds. Its configuration can't be changed later.
	op := &oto.NewContextOptions{
		SampleRate:   44100,
		ChannelCount: 1, // Stereo output
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
	fileBytesReader := bytes.NewReader(file)
	// Decode file. This process is done as the file plays so it won't
	// load the whole thing into memory.
	decodedMp3, err := mp3.NewDecoder(fileBytesReader)
	if err != nil {
		panic("mp3.NewDecoder failed: " + err.Error())
	}
	player := otoCtx.NewPlayer(decodedMp3)

	player.SetVolume(volume)

	// Play starts playing the sound and returns without waiting for it (Play() is async).
	player.Play()

	// We can wait for the sound to finish playing using something like this
	for player.IsPlaying() {
		time.Sleep(time.Millisecond)
	}

	// If you don't want the player/sound anymore simply close
	err = player.Close()
	if err != nil {
		panic("player.Close failed: " + err.Error())
	}

}

func checkStreamStatus(channel string) (string, bool) {
	// url := "https://www.twitch.tv/" + channel
	url := "https://decapi.me/twitch/uptime/" + channel

	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("Error fetching the URL: %v\n", err)
		return "error", false
	}
	defer resp.Body.Close()

	// Check for a successful HTTP status
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Failed to fetch the URL. HTTP Status: %s\n", resp.Status)
		return "error", false
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response body: %v\n", err)
		return "error", false
	}

	// Check if the stream is live? might break in the future
	// if strings.Contains(string(body), `isLiveBroadcast`) {
	// 	return channel, true
	// } else {
	// 	return channel, false
	// }

	// for decapi
	// Check if the stream is live? might break in the future
	if strings.Contains(string(body), `offline`) {
		// fmt.Println(channel, "is live!")
		return channel, false
	} else {
		// fmt.Println(channel, "is offline!")
		return channel, true
	}
}

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

		streamElementsUrl := fmt.Sprintf("https://api.streamelements.com/kappa/v2/speech?voice=Brian&text=%s is now live.", channel)
		resp, err := http.Get(streamElementsUrl)
		if err != nil {
			fmt.Printf("Error fetching the URL: %v\n", err)
			return nil
		}
		defer resp.Body.Close()

		// Check for a successful HTTP status
		if resp.StatusCode != http.StatusOK {
			fmt.Printf("Failed to fetch the URL. HTTP Status: %s\n", resp.Status)
			return nil
		}

		// Read the response body
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

// Create the tts whitelist string
func createTtsString(filePath string) string {
	list := getChannelsFromFile(filePath)
	var buffer bytes.Buffer
	for _, v := range list {
		buffer.WriteString(v + ";")
	}
	return buffer.String()
}

// Initialize the slice of channel statuses
func initStreamsSlice() []ChannelStatus {
	var channelStatuses []ChannelStatus
	for _, channel := range channels {
		_, status := checkStreamStatus(channel)
		channelStatuses = append(channelStatuses, ChannelStatus{
			Name:        channel,
			IsLive:      status,
			HasPlayed:   false,
			LastChanged: time.Now(),
		})
	}
	return channelStatuses
}

// Print the slice of channel statuses
func printSlice(channels []ChannelStatus) {
	for _, channel := range channels {
		if channel.IsLive { // If live
			fmt.Printf("%-17s -> %s[ %s ]%7s hasPlayed: %s%t%s\n",
				channel.Name, Green, "LIVE", Reset, Cyan, channel.HasPlayed, Reset)
		} else { // If offline
			continue
			fmt.Printf("%-17s -> %s[ %s ]%s hasPlayed: %s%t%s\n",
				channel.Name, Red, "OFFLINE", Reset, Cyan, channel.HasPlayed, Reset)
		}
	}
}

func main() {
	streamsFile := "streams.txt"
	fmt.Printf("checksum: %x\n", getFileChecksum(streamsFile))
	channels = getChannelsFromFile(streamsFile)
	otoCtx, _ = initOto()

	for _, channel := range channels {
		fmt.Println(channel)
	}

	// Initialize the slice
	channelStatuses := initStreamsSlice()
	printSlice(channelStatuses)

	ttsString := createTtsString("tts.txt")
	fmt.Println(ttsString)

	fmt.Println("Attempting to pre-generates tts files...")
	time.Sleep(2000)
	ttsFiles := getChannelsFromFile("tts.txt")
	for _, v := range ttsFiles {
		fmt.Println("Checking ", v)
		getMp3ForChannel(v)
	}
	time.Sleep(4000)

	// Enter the for loop
	for {
		for i := range channelStatuses {
			// Check the live status
			_, isOnline := checkStreamStatus(channelStatuses[i].Name)
			time.Sleep(time.Millisecond * 100)
			currentTime := time.Now()

			if channelStatuses[i].IsLive && !channelStatuses[i].HasPlayed {
				// If live but hasnt played
				if strings.Contains(ttsString, channelStatuses[i].Name) {
					mp3 := getMp3ForChannel(channelStatuses[i].Name)
					fmt.Printf("Playing mp3 from local file %s.mp3\n", channelStatuses[i].Name)
					playMp3(mp3, 0.10)
					channelStatuses[i].HasPlayed = true
				}
			} else if channelStatuses[i].IsLive == isOnline {
				// If status hasnt changed, move on
				continue
			} else if !channelStatuses[i].IsLive && isOnline {
				// If previously offline but now online
				if currentTime.Sub(channelStatuses[i].LastChanged) > 5*time.Minute {
					// If enough time passed (+5 minutes)
					if strings.Contains(ttsString, channelStatuses[i].Name) {
						mp3 := getMp3ForChannel(channelStatuses[i].Name)
						fmt.Printf("Playing mp3 from local file %s.mp3\n", channelStatuses[i].Name)
						playMp3(mp3, 0.10)
						channelStatuses[i].HasPlayed = true

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

		// Clear the screen - windows only
		c := exec.Command("clear")
		c.Stdout = os.Stdout
		c.Run()

		// Print the updated statuses
		printSlice(channelStatuses)
		fmt.Println("Waiting 5 miutes...")
		time.Sleep(5 * time.Minute)
	}
}
