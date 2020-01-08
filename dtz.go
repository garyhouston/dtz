package main

import (
	mwclient "cgt.name/pkg/go-mwclient"
	"cgt.name/pkg/go-mwclient/params"
	"fmt"
	"github.com/antonholmquist/jason"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func writeString(w io.Writer, s string) error {
	_, err := w.Write([]byte(s))
	return err
}

func writeLink(w io.Writer, url, text string) error {
	_, err := fmt.Fprintf(w, "<a href=\"%s\">%s</a>", url, text)
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

const commonsPrefix = "https://commons.wikimedia.org/wiki/"
const oauthManageURL = "https://www.mediawiki.org/wiki/Special:OAuthManageMyGrants"

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/dtz" && r.URL.Path != "/dtz/" {
		http.Redirect(w, r, "/dtz/", http.StatusSeeOther)
		return
	}
	writeHead(w, "dtz")
	writeString(w, "<body>\n")
	writeString(w, "<p>This is a tool under development, it doesn't work yet.</p>")
	writeString(w, "<p>This tool edits the dates of files on Wikimedia Commons, using the \n")
	writeLink(w, commonsPrefix+"Template:DTZ", "DTZ")
	writeString(w, " template to display the dates with timezones. The date/times are taken from Exif and adjusted by the difference between the tizezone set in the camera and timezones at the place the image was created, as specified below.</p>")
	writeString(w, "<form action=\"/dtz/output\" method=\"post\">\n")
	writeString(w, "<p>Timezones can be specified either as a number, in the format HHMM, or as the name of a timezone from the TZ database.\n")
	writeString(w, "Using the TZ timezones will automatically adjust for daylight savings.\n")
	writeString(w, "The TZ names have the format \"Africa/Abidjan\"; a list can be found at \n")
	writeLink(w, "https://en.wikipedia.org/wiki/List_of_tz_database_time_zones", "List of tz database time zones")
	writeString(w, ".\n")
	writeString(w, "A numerical value can be positive for eastern timezones and negative for western.\n")
	writeString(w, "E.g., 1000 for Eastern Australia without daylight savings, or -800 for North American Pacific Time without daylight savings.</p>\n")
	writeString(w, "<p>Camera timezone <input type=\"text\" name=\"camera\" size=\"50\"><br>\n")
	writeString(w, "Location timezone <input type=\"text\" name=\"location\" size=\"50\"></p>\n")
	writeString(w, "<p>Either a single file or a range of files can be edited. A range is obtained by using the upload order from the relevant user on Commons between the two specified files. The order doesn't matter. Note that if multiple files have the same upload timestamp as either the first or last file, all will be processed.\n</p>")
	writeString(w, "<p>First file in range <input type=\"text\" name=\"first\" size=\"60\"><br>\n")
	writeString(w, "Last file in range <input type=\"text\" name=\"last\" size=\"60\"></p>\n")
	writeString(w, "If filters are specified, files will only be processed if the text appears as a substring in either the wiki source of the author field, or in the camera model in Exif. The matching is case insensitive.</p>")
	writeString(w, "<p>Author filter <input type=\"text\" name=\"author\" size=\"50\"><br>\n")
	writeString(w, "Camera model filter <input type=\"text\" name=\"model\" size=\"50\"><br>\n")
	writeString(w, "<input type=\"submit\" value=\"Submit\"></p>\n")
	writeString(w, "</form>\n")
	writeString(w, "</body></html>")
}

type zoneInfo struct {
	numeric bool
	mins    int
	loc     *time.Location
}

type imageInfo struct {
	uploadTime, user, origTime string
}

func extractInfo(page *jason.Object) (imageInfo, error) {
	noinfo := imageInfo{}
	obj, err := page.Object()
	if err != nil {
		return noinfo, err
	}
	missing, err := obj.GetBoolean("missing")
	if err == nil && missing {
		return noinfo, fmt.Errorf("File not found")
	}
	infoArray, err := obj.GetObjectArray("imageinfo")
	if err != nil {
		return noinfo, err
	}
	info, err := infoArray[0].Object()
	if err != nil {
		return noinfo, err
	}
	var result imageInfo
	result.uploadTime, err = info.GetString("timestamp")
	if err != nil {
		return noinfo, err
	}
	result.user, err = info.GetString("user")
	if err != nil {
		return noinfo, err
	}
	metadata, err := infoArray[0].GetObjectArray("commonmetadata")
	if err != nil {
		// metadata is null in some cases
		return result, nil
	}
	for i := 0; i < len(metadata); i++ {
		name, err := metadata[i].GetString("name")
		if err != nil {
			return noinfo, err
		}
		if name == "DateTimeOriginal" {
			result.origTime, err = metadata[i].GetString("value")
			if err != nil {
				return noinfo, err
			}
			break
		}
	}
	return result, nil
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

func getImageInfo(first, last string, client *mwclient.Client, w http.ResponseWriter) (imageInfo, imageInfo, error) {
	noinfo := imageInfo{}
	params := params.Values{
		"action":   "query",
		"titles":   first + "|" + last,
		"prop":     "imageinfo",
		"iiprop":   "timestamp|user|commonmetadata",
		"continue": "",
	}
	json, err := client.Get(params)
	if err != nil {
		return noinfo, noinfo, err
	}
	pages, err := json.GetObjectArray("query", "pages")
	if err != nil {
		return noinfo, noinfo, err
	}
	if len(pages) < 1 {
		return noinfo, noinfo, fmt.Errorf("Empty pages array when requesting imageinfo")
	}
	imageInfo1, err := extractInfo(pages[0])
	if err != nil {
		return noinfo, noinfo, err
	}
	if len(pages) < 2 {
		// If we requested the same file twice, we only get one response.
		return imageInfo1, imageInfo1, nil
	}
	imageInfo2, err := extractInfo(pages[1])
	if err != nil {
		return noinfo, noinfo, err
	}
	return imageInfo1, imageInfo2, nil
}

const batchSize = 100

func printTitle(w http.ResponseWriter, title string) {
	writeLink(w, commonsPrefix+url.PathEscape(title), title)
	writeString(w, " &mdash; ")
}

func processRange(uploadTime1, uploadTime2, user string, cameraZone, localZone zoneInfo, client *mwclient.Client, w http.ResponseWriter) {
	writeString(w, "<p>To stop this thing, press the browser stop button, close the page, or revoke OAuth access at ")
	writeLink(w, oauthManageURL, "Special:OAuthManageMyGrants")
	writeString(w, ".</p><p>\n")
	params := params.Values{
		"generator": "allimages",
		"gaiuser":   user,
		"gaisort":   "timestamp",
		"gaidir":    "ascending",
		"gailimit":  strconv.Itoa(batchSize),
		"prop":      "imageinfo",
		"iiprop":    "commonmetadata",
		"gaistart":  uploadTime1,
		"gaiend":  uploadTime2,
	}
	flusher, haveFlush := w.(http.Flusher)
	if !haveFlush {
		writeString(w, "Expected a flush method.")
		return
	}
	query := client.NewQuery(params)
queryLoop:
	for query.Next() {
		json := query.Resp()
		pages, err := json.GetObjectArray("query", "pages")
		if err != nil {
			writeString(w, "Skipped a batch with missing pages array.<br>\n")
			continue
		}
		if len(pages) == 0 {
			break
		}
		for i, _ := range pages {
			time.Sleep(time.Duration(1) * time.Second)
			flusher.Flush()
			obj, err := pages[i].Object()
			if err != nil {
				writeString(w, "Skipped an item with missing pages object.<br>\n")
				continue
			}
			title, err := obj.GetString("title")
			if err != nil {
				printTitle(w, title)
				writeString(w, "Skipped an item with no title.<br>\n")
				continue
			}
			infoArray, err := obj.GetObjectArray("imageinfo")
			if err != nil {
				printTitle(w, title)
				writeString(w, "missing imageinfo array.<br>\n")
				continue
			}
			printTitle(w, title)
			metadata, err := infoArray[0].GetObjectArray("commonmetadata")
			if err != nil {
				writeString(w, "no commonmetadata.<br>\n")
				continue
			}
			var origTime string
			for i := 0; i < len(metadata); i++ {
				name, err := metadata[i].GetString("name")
				if err != nil {
					continue
				}
				if name == "DateTimeOriginal" {
					origTime, err = metadata[i].GetString("value")
					if err != nil {
						continue
					}
					break
				}
			}
			if origTime == "" {
				writeString(w, "time not found in metadata.<br>\n")
				continue
			}
			err = writeString(w, "date-time: "+origTime+"<br>\n")
			if err != nil {
				// Presumably lost the connection to the browser.
				break queryLoop
			}
		}
	}
	if query.Err() != nil {
		writeString(w, "Query returned an error: "+query.Err().Error()+"<br>")
	}
	writeString(w, "</body></html>")
}

func dateParam(param string) (zoneInfo, error) {
	var nozone zoneInfo
	if param == "" {
		return nozone, fmt.Errorf("Please set both of the timezone parameters.")
	}
	var result zoneInfo
	var err error
	result.mins, err = strconv.Atoi(param)
	result.numeric = err == nil
	if err != nil {
		if strings.Index(param, "/") == -1 {
			return nozone, fmt.Errorf("Timezone should be either numeric or a tz database zone name.")
		}
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
	if last == filePrefix {
		last = first
	}
	imageInfo1, imageInfo2, err := getImageInfo(first, last, client, w)
	if err != nil {
		preError(w, title, err)
		return
	}
	if imageInfo1.uploadTime > imageInfo2.uploadTime {
		tmp := imageInfo1
		imageInfo1 = imageInfo2
		imageInfo2 = tmp
	}
	if imageInfo1.user != imageInfo2.user {
		preMessage(w, title, "Two files must be uploaded by the same user.\n")
		return
	}
	if author != "" || model != "" {
		preMessage(w, title, "Filter fields not yet supported.")
		return
	}
	writeHead(w, title)
	writeString(w, "<body>\n")
	processRange(imageInfo1.uploadTime, imageInfo2.uploadTime, imageInfo1.user, cameraZone, localZone, client, w)
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
