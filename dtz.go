package main

import (
	mwclient "cgt.name/pkg/go-mwclient"
	"cgt.name/pkg/go-mwclient/params"
	"fmt"
	"github.com/antonholmquist/jason"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func writeString(w io.Writer, s string) error {
	_, err := w.Write([]byte(s))
	return err
}

func writeHead(w http.ResponseWriter, title string) {
	writeString(w, "<!DOCTYPE html>\n")
	writeString(w, "<html lang=\"en\"><head><title>")
	writeString(w, title)
	writeString(w, "</title></head>\n")
}

func preError(w http.ResponseWriter, title string, err error) {
	writeHead(w, title)
	writeString(w, "<body>\n")
	writeString(w, err.Error())
	writeString(w, "</body></html>")
}

func preMessage(w http.ResponseWriter, title, msg string) {
	writeHead(w, title)
	writeString(w, "<body>\n")
	writeString(w, msg)
	writeString(w, "</body></html>")
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/dtz" && r.URL.Path != "/dtz/" {
		http.Redirect(w, r, "/dtz/", http.StatusSeeOther)
		return
	}
	writeHead(w, "dtz")
	writeString(w, "<body>\n")
	writeString(w, "<p>This tool edits the date of files on Wikimedia Commons, based on the dates in Exif and the timezones specified below. It's currently a work in progress.</p>\n")
	writeString(w, "<form action=\"/dtz/output\" method=\"post\">\n")
	writeString(w, "Camera timezone <input type=\"text\" name=\"camera\" size=\"10\"><br>\n")
	writeString(w, "Location timezone <input type=\"text\" name=\"location\" size=\"10\"><br>\n")
	writeString(w, "First file in range <input type=\"text\" name=\"first\" size=\"60\"><br>\n")
	writeString(w, "Last file in range <input type=\"text\" name=\"last\" size=\"60\"><br>\n")
	writeString(w, "Author filter <input type=\"text\" name=\"author\" size=\"50\"><br>\n")
	writeString(w, "Camera model filter <input type=\"text\" name=\"model\" size=\"50\"><br>\n")
	writeString(w, "<input type=\"submit\" value=\"Submit\">\n")
	writeString(w, "</form>\n")
	writeString(w, "</body></html>")
}

type zoneInfo struct {
	numeric bool
	mins    int
	loc     *time.Location
}

func editOne(file string, cameraZone, localZone zoneInfo, client *mwclient.Client) error {
	return nil
}

func extractInfo(page *jason.Object) (string, string, error) {
	obj, err := page.Object()
	if err != nil {
		return "", "", err
	}
	missing, err := obj.GetBoolean("missing")
	if err == nil && missing {
		return "", "", fmt.Errorf("File not found")
	}
	imageInfoArray, err := obj.GetObjectArray("imageinfo")
	if err != nil {
		return "", "", err
	}
	imageInfo, err := imageInfoArray[0].Object()
	if err != nil {
		return "", "", err
	}
	timestamp, err := imageInfo.GetString("timestamp")
	if err != nil {
		return "", "", err
	}
	user, err := imageInfo.GetString("user")
	if err != nil {
		return "", "", err
	}
	return timestamp, user, nil
}

func oauth(client *mwclient.Client) error {
	consumerToken := os.Getenv("ConsumerToken")
	consumerSecret := os.Getenv("ConsumerSecret")
	accessToken := os.Getenv("AccessToken")
	accessSecret := os.Getenv("AccessSecret")
	if consumerToken == "" || consumerSecret == "" || accessToken == "" || accessSecret == "" {
		return fmt.Errorf("OAuth tokens not set")
	}
	return client.OAuth(consumerToken, consumerSecret, accessToken, accessSecret)
}

func getImageInfo(first, last string, client *mwclient.Client, w http.ResponseWriter) (string, string, string, string, error) {
	params := params.Values{
		"action":   "query",
		"titles":   first + "|" + last,
		"prop":     "imageinfo",
		"continue": "",
	}
	json, err := client.Get(params)
	if err != nil {
		return "", "", "", "", err
	}
	pages, err := json.GetObjectArray("query", "pages")
	if err != nil {
		return "", "", "", "", err
	}
	if len(pages) < 2 {
		return "", "", "", "", fmt.Errorf("Short pages array when requesting imageinfo")
	}
	timestamp1, user1, err := extractInfo(pages[0])
	if err != nil {
		return "", "", "", "", err
	}
	timestamp2, user2, err := extractInfo(pages[1])
	if err != nil {
		return "", "", "", "", err
	}
	return timestamp1, timestamp2, user1, user2, nil
}

func dateParam(param string) (zoneInfo, error) {
	if param == "" {
		return zoneInfo{}, fmt.Errorf("Please set both of the timezone parameters.")
	}
	var result zoneInfo
	var err error
	result.mins, err = strconv.Atoi(param)
	result.numeric = err == nil
	if err != nil {
		result.loc, err = time.LoadLocation(param)
	}
	return result, err
}

func trimmedField(field string, r *http.Request) string {
	return strings.TrimSpace(r.Form.Get(field))
}

func outputHandler(w http.ResponseWriter, r *http.Request) {
	title := "dtz output"
	err := r.ParseForm()
	if err != nil {
		preError(w, title, err)
		return
	}
	client, err := mwclient.New("https://commons.wikimedia.org/w/api.php", "dtz; User:Ghouston")
	if err != nil {
		preError(w, title, err)
		return
	}
	client.Maxlag.On = true
	err = oauth(client)
	if err != nil {
		preError(w, title, err)
		return
	}
	cameraZone, err := dateParam(trimmedField("camera", r))
	if err != nil {
		preError(w, title, err)
		return
	}
	localZone, err := dateParam(trimmedField("location", r))
	if err != nil {
		preError(w, title, err)
		return
	}
	filePrefix := "File:"
	prefixLen := len(filePrefix)
	first := trimmedField("first", r)
	if len(first) < prefixLen || first[0:prefixLen] != filePrefix {
		first = filePrefix + first
	}
	last := trimmedField("last", r)
	if len(last) < prefixLen || last[0:prefixLen] != filePrefix {
		last = filePrefix + last
	}
	author := trimmedField("author", r)
	model := trimmedField("model", r)
	if first == filePrefix {
		first = last
	}
	if first == filePrefix {
		preMessage(w, title, "Please supply at least one file name.\n")
		return
	}
	if last == filePrefix || first == last {
		err = editOne(first, cameraZone, localZone, client)
		if err != nil {
			preError(w, title, err)
		}
		preMessage(w, title, "Will process one file only.")
		return
	}
	timestamp1, timestamp2, user1, user2, err := getImageInfo(first, last, client, w)
	if err != nil {
		preError(w, title, err)
		return
	}
	if timestamp1 > timestamp2 {
		tmp := timestamp1
		timestamp1 = timestamp2
		timestamp2 = tmp
	}
	if user1 != user2 {
		preMessage(w, title, "Two files must be uploaded by the same user.\n")
	}
	if author != "" || model != "" {
	}
	writeHead(w, title)
	writeString(w, "<body>\n")
	fmt.Fprintf(w, "<p>Will process the range %s - %s for user %s.</p>\n", timestamp1, timestamp2, user1)
	writeString(w, "</body></html>")
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		fmt.Println("PORT not set in environment")
		return
	}
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/dtz/output", outputHandler)

	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
}
