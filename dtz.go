package main

import (
	mwclient "cgt.name/pkg/go-mwclient"
	"cgt.name/pkg/go-mwclient/params"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"github.com/antonholmquist/jason"
	"github.com/mrjones/oauth"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"regexp"
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
const oauthRequestURL = "https://www.mediawiki.org/wiki/Special:OAuth/initiate"
const oauthAuthorizeURL = "https://www.mediawiki.org/wiki/Special:OAuth/authorize"
const oauthAccessURL = "https://www.mediawiki.org/wiki/Special:OAuth/token"
const oauthManageURL = "https://www.mediawiki.org/wiki/Special:OAuthManageMyGrants"
const gitURL = "https://github.com/garyhouston/dtz"
const talkURL = "https://commons.wikimedia.org/wiki/User_talk:Ghouston"

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/dtz" && r.URL.Path != "/dtz/" {
		http.Redirect(w, r, "/dtz/", http.StatusSeeOther)
		return
	}
	title := "dtz"
	err := r.ParseForm()
	if err != nil {
		preError(w, title, err)
		return
	}
	oauthToken := r.Form.Get("oauth_token")
	oauthVerifier := r.Form.Get("oauth_verifier")
	writeHead(w, "dtz")
	writeString(w, "<body>\n")
	writeString(w, "<p>This is a tool under development. For discussion, use the source code repository at ")
	writeLink(w, gitURL, "github")
	writeString(w, " or the author's ")
	writeLink(w, talkURL, "talk page")
	writeString(w, " at Wikimedia Commons.</p>\n")
	writeString(w, "<p>Enable this application with OAuth: ")
	writeLink(w, "/dtz/auth", "authorize")
	writeString(w, "</p>\n")
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
	writeString(w, "If filters are specified, files will only be processed if the text appears as a substring in either the wiki source of the author field, or in the camera model in Exif. The matching is case insensitive. Only the first line of the author field is examined.</p>")
	writeString(w, "<p>Author filter <input type=\"text\" name=\"author\" size=\"50\"><br>\n")
	writeString(w, "Camera model filter <input type=\"text\" name=\"model\" size=\"50\"></p>\n")
	writeString(w, "<p>After pressing Submit, it may take some time before output appears. Edits are limited to one per five seconds, and can be examined in real-time at your contributions page at Commons. If you need to stop the tool, press the browser stop button, close the page, or revoke OAuth access at ")
	writeLink(w, oauthManageURL, "Special:OAuthManageMyGrants")
	writeString(w, "</p>\n")
	writeString(w, "<input type=\"submit\" value=\"Submit\">\n")
	fmt.Fprintf(w, "<input type=\"hidden\" name=\"oauthtoken\" value=\"%s\">\n", oauthToken)
	fmt.Fprintf(w, "<input type=\"hidden\" name=\"oauthverifier\" value=\"%s\">\n", oauthVerifier)
	writeString(w, "</form>\n")
	writeString(w, "</body></html>")
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

// Replace non-parsed sections in text, such as <!-- ... --> blocks, with spaces.
func blankNonParsedSections(text string) string {
	// Assume that unparsed sections don't nest, but don't assume
	// that a matching end tag is present.
	text = strings.ToLower(text) // Ignore tag case.
	startTags := []string{"<!--", "<nowiki>", "<pre>", "<math>"}
	endTags := []string{"-->", "</nowiki>", "</pre>", "</math>"}
	start := -1
	startTag := ""
	endTag := ""
	// Find the first non-parsed section, if any.
	for i := 0; i < len(startTags); i++ {
		pos := strings.Index(text, startTags[i])
		if pos >= 0 && (start == -1 || pos < start) {
			start = pos
			startTag = startTags[i]
			endTag = endTags[i]
		}

	}
	if start >= 0 {
		// Blank out the non-parsed section.
		unterminated := false
		startTagLen := len(startTag)
		end := strings.Index(text[start+startTagLen:], endTag)
		if end == -1 {
			end = len(text)
			unterminated = true
		} else {
			end += start + startTagLen + len(endTag)
		}
		text = text[:start] + strings.Repeat(" ", end-start) + text[end:]
		if !unterminated {
			return blankNonParsedSections(text)
		}
	}
	return text
}

func findField(text, field string) (int, int) {
	regexp := regexp.MustCompile("\\|\\s*" + field + "\\s*=")
	match := regexp.FindStringIndex(text)
	if match == nil {
		return -1, -1
	}
	start := match[1]
	// Just look at the first line of the field. Parsing multi-line fields
	// properly may be difficult.
	lineLen := strings.Index(text[start:], "\n")
	if lineLen == -1 {
		// Text truncated?
		return start, len(text)
	} else {
		return start, start + lineLen
	}
}

// Find the Author and Date fields in page text.
func findPositions(text string) (int, int, int, int) {
	text = blankNonParsedSections(text)
	p1, p2 := findField(text, "author")
	p3, p4 := findField(text, "date")
	return p1, p2, p3, p4
}

func edit(title string, newDate time.Time, lastEdit *time.Time, authorFilter string, client *mwclient.Client) error {
	// Don't attempt to edit more than once per 5 seconds, per Commons bot policy
	dur := time.Since(*lastEdit)
	if dur.Seconds() < 5 {
		time.Sleep(time.Duration(5)*time.Second - dur)
	}
	// There's a small chance that saving a page may fail due to
	// an edit conflict or other transient error. Try up to 3
	// times before giving up.
	var saveError error
	for i := 0; i < 3; i++ {
		text, timestamp, err := client.GetPageByName(title)
		if err != nil {
			return err
		}
		authorStart, authorEnd, dateStart, dateEnd := findPositions(text)
		if authorFilter != "" {
			if authorStart == -1 || strings.Index(strings.ToLower(text[authorStart:authorEnd]), authorFilter) == -1 {
				return fmt.Errorf("author didn't match.")
			}
		}
		dateStr := fmt.Sprintf("{{DTZ|%s}}", newDate.Format("2006-01-02T15:04:05-07"))
		newText := text[:dateStart] + dateStr + text[dateEnd:]
		if newText == text {
			return fmt.Errorf("no change needed")
		}
		editcfg := map[string]string{
			"action":        "edit",
			"title":         title,
			"text":          newText,
			"summary":       "Set date from Exif with time zone",
			"basetimestamp": timestamp,
		}
		saveError = client.Edit(editcfg)
		if saveError == nil {
			break
		}
	}
	if saveError != nil {
		return fmt.Errorf("failed to save: %v", saveError)
	}
	*lastEdit = time.Now()
	return nil
}

func printTitle(w http.ResponseWriter, title string) {
	writeLink(w, commonsPrefix+url.PathEscape(title), title)
	writeString(w, " &mdash; ")
}

func processRange(uploadTime1, uploadTime2, user string, cameraZone, localZone *time.Location, authorFilter, modelFilter string, client *mwclient.Client, w http.ResponseWriter) {
	writeString(w, "<p>To stop this tool, press the browser stop button, close the page, or revoke OAuth access at ")
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
		"gaiend":    uploadTime2,
	}
	flusher, haveFlush := w.(http.Flusher)
	if !haveFlush {
		writeString(w, "Expected a flush method.")
		return
	}
	var lastEdit time.Time
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
			var model string
			for i := 0; i < len(metadata); i++ {
				name, err := metadata[i].GetString("name")
				if err != nil {
					continue
				}
				if name == "DateTimeOriginal" {
					origTime, _ = metadata[i].GetString("value")
				} else if name == "Model" {
					model, _ = metadata[i].GetString("value")
					if err != nil {
						continue
					}
				}
			}
			if origTime == "" {
				writeString(w, "time not found in metadata.<br>\n")
				continue
			}
			if modelFilter != "" {
				if model == "" || strings.Index(strings.ToLower(model), modelFilter) == -1 {
					writeString(w, "camera model didn't match.<br>\n")
					continue
				}
			}
			timeStampFormat := "2006:01:02 15:04:05"
			origTimeParsed, err := time.ParseInLocation(timeStampFormat, origTime, cameraZone)
			if err != nil {
				writeString(w, "failed to parse the timestamp: ")
				writeString(w, err.Error())
				writeString(w, "<br>\n")
				continue
			}
			origTimeConverted := origTimeParsed.In(localZone)
			err = edit(title, origTimeConverted, &lastEdit, authorFilter, client)
			if err != nil {
				writeString(w, err.Error()+"<br>\n")
				continue
			}
			err = writeString(w, "date-time "+origTimeParsed.Format(timeStampFormat)+" converted to "+origTimeConverted.Format(timeStampFormat)+"<br>\n")
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

func dateParam(param string) (*time.Location, error) {
	if param == "" {
		return nil, fmt.Errorf("Please set both of the timezone parameters.")
	}
	num, err := strconv.Atoi(param)
	if err == nil {
		hours := num / 100
		mins := num % 100
		return time.FixedZone(param, (hours*60+mins)*60), nil
	}
	if strings.Index(param, "/") == -1 {
		return nil, fmt.Errorf("Timezone should be either numeric or a tz database zone name.")
	}
	return time.LoadLocation(param)
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
	oauthToken := r.Form.Get("oauthtoken")
	oauthVerifier := r.Form.Get("oauthverifier")
	if oauthToken != "" && oauthVerifier != "" {
		err = authClient(client, oauthToken, oauthVerifier)
		if err != nil {
			preError(w, title, err)
			return
		}
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
	authorFilter := strings.ToLower(trimmedField("author", r))
	modelFilter := strings.ToLower(trimmedField("model", r))
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
	writeHead(w, title)
	writeString(w, "<body>\n")
	processRange(imageInfo1.uploadTime, imageInfo2.uploadTime, imageInfo1.user, cameraZone, localZone, authorFilter, modelFilter, client, w)
}

func loadPrivateKey() (*rsa.PrivateKey, error) {
	keyFile := os.Getenv("PrivateKeyFile")
	if keyFile == "" {
		return nil, fmt.Errorf("PrivateKeyFile not set in environment")
	}
	bytes, err := ioutil.ReadFile(keyFile)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(bytes)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("Failed to parse PEM private key")
	}
	pk, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	pkey, ok := pk.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("Failed to typecast public key")
	}
	return pkey, nil
}

func authHandler(w http.ResponseWriter, r *http.Request) {
	title := "dtz auth"
	err := r.ParseForm()
	if err != nil {
		preError(w, title, err)
		return
	}
	consumerToken := os.Getenv("ConsumerToken")
	if consumerToken == "" {
		preMessage(w, title, "OAuth consumer token not set in environment")
		return
	}
	rsaKey, err := loadPrivateKey()
	if err != nil {
		preError(w, title, err)
		return
	}
	consumer := oauth.NewRSAConsumer(
		consumerToken,
		rsaKey,
		oauth.ServiceProvider{RequestTokenUrl: oauthRequestURL, AuthorizeTokenUrl: oauthAuthorizeURL, AccessTokenUrl: oauthAccessURL})
	/*requestToken*/ _, url, err := consumer.GetRequestTokenAndUrl("oob")
	if err != nil {
		preError(w, title, err)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

func authClient(client *mwclient.Client, oauthToken, oauthVerifier string) error {

	consumerToken := os.Getenv("ConsumerToken")
	if consumerToken == "" {
		return fmt.Errorf("OAuth consumer token not set in environment")
	}
	rsaKey, err := loadPrivateKey()
	if err != nil {
		return err
	}
	consumer := oauth.NewRSAConsumer(consumerToken, rsaKey, oauth.ServiceProvider{RequestTokenUrl: oauthRequestURL, AuthorizeTokenUrl: oauthAuthorizeURL, AccessTokenUrl: oauthAccessURL})
	access, err := consumer.AuthorizeToken(&oauth.RequestToken{Token: oauthToken}, oauthVerifier)
	if err != nil {
		return err
	}
	httpc, err := consumer.MakeHttpClient(access)
	if err != nil {
		return err
	}
	return client.ReplaceHTTPC(httpc)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		fmt.Println("PORT not set in environment")
		return
	}
	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/dtz/output", outputHandler)
	http.HandleFunc("/dtz/auth", authHandler)

	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
}
