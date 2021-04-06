package main

import (
	mwclient "cgt.name/pkg/go-mwclient"
	"cgt.name/pkg/go-mwclient/params"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/antonholmquist/jason"
	"github.com/dgrijalva/jwt-go"
	"github.com/mrjones/oauth"
	"html"
	"io"
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
	_, err := fmt.Fprintf(w, `<a href="%s">%s</a>`, url, text)
	return err
}

func writeHead(w http.ResponseWriter, title string) {
	writeString(w, `<!DOCTYPE html>
<html lang="en"><head><title>`)
	writeString(w, title)
	writeString(w, "</title></head>\n")
}

func preError(w http.ResponseWriter, title string, err error) {
	writeHead(w, title)
	writeString(w, "<body>\n")
	writeString(w, html.EscapeString(err.Error()))
	writeString(w, "\n</body></html>")
}

func preMessage(w http.ResponseWriter, title, msg string) {
	writeHead(w, title)
	writeString(w, "<body>\n")
	writeString(w, html.EscapeString(msg))
	writeString(w, "\n</body></html>")
}

const commonsPrefix = "https://commons.wikimedia.org/"
const commonsWiki = commonsPrefix + "wiki/"
const oauthRequestURL = "https://www.mediawiki.org/wiki/Special:OAuth/initiate"
const oauthAuthorizeURL = "https://www.mediawiki.org/wiki/Special:OAuth/authorize"
const oauthAccessURL = "https://www.mediawiki.org/wiki/Special:OAuth/token"
const oauthManageURL = "https://www.mediawiki.org/wiki/Special:OAuthManageMyGrants"
const gitURL = "https://github.com/garyhouston/dtz"
const talkURL = commonsWiki + "User_talk:Ghouston"
const toolRelative = "/"
const outputRelative = toolRelative + "output"
const authRelative = toolRelative + "auth"
const logoutRelative = toolRelative + "logout"
const tokenCookie = "dtz_token"
const secretCookie = "dtz_secret"

// Immutable once loaded in main().
var privateKey *rsa.PrivateKey

func rootHandler(w http.ResponseWriter, r *http.Request) {
	title := "dtz"
	err := r.ParseForm()
	if err != nil {
		preError(w, title, err)
		return
	}
	oauthToken := r.Form.Get("oauth_token")
	oauthVerifier := r.Form.Get("oauth_verifier")
	if oauthToken != "" && oauthVerifier != "" {
		accessToken, accessSecret, err := authGetAccess(oauthToken, oauthVerifier)
		if err != nil {
			preError(w, title, err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: tokenCookie, Path: toolRelative, Value: accessToken, HttpOnly: true, Secure: true})
		http.SetCookie(w, &http.Cookie{Name: secretCookie, Path: toolRelative, Value: accessSecret, HttpOnly: true, Secure: true})
		http.Redirect(w, r, toolRelative, http.StatusSeeOther)
		return
	}
	if r.URL.Path != toolRelative {
		http.Redirect(w, r, toolRelative, http.StatusSeeOther)
		return
	}
	writeHead(w, "dtz")
	writeString(w, "<body>\n")
	writeString(w, "<p>DTZ: Set dates and times of files on Commons &mdash;\n")
	writeLink(w, gitURL, "source code at github")
	writeString(w, "\n&mdash; ")
	writeLink(w, talkURL, "author's talk page")
	writeString(w, ".</p>\n")
	if _, err := r.Cookie(tokenCookie); err == nil {
		writeString(w, "<p>OAuth appears to be enabled. ")
		writeLink(w, logoutRelative, "Logout")
		writeString(w, "</p>\n")
	} else {
		writeString(w, "<p>Enable this application with OAuth: ")
		writeLink(w, authRelative, "authorize")
		writeString(w, "</p>\n")
	}
	writeString(w, "<p>This tool edits the dates and times of files on Wikimedia Commons,\nusing the ")
	writeLink(w, commonsWiki+"Template:DTZ", "DTZ")
	writeString(w, ` template to display timezones.
The date/times are taken from Exif and adjusted by the difference between the tizezone set in the camera
and the timezone at the place the image was created, as specified below.</p>
`)
	writeString(w, `<form action="`)
	writeString(w, outputRelative)
	writeString(w, `" method="post">
<p>Timezones can be specified either as a number, in the format HHMM, or as the name of a timezone from the TZ
database. Using the TZ timezones will automatically adjust for daylight savings. The TZ names have the format
"Africa/Abidjan"; a list can be found at
`)
	writeLink(w, "https://en.wikipedia.org/wiki/List_of_tz_database_time_zones", "List of tz database time zones")
	writeString(w, `.
A numerical value can be positive for eastern timezones and negative for western. E.g., 1000 for Eastern
Australia without daylight savings, or -800 for North American Pacific Time without daylight savings.</p>
<p>Camera timezone <input type="text" name="camera" size="50"><br>
Location timezone <input type="text" name="location" size="50"></p>
<p>Either a single file or a range of files can be edited. A range is obtained by using the upload order
from the relevant user on Commons between the two specified files. The order doesn't matter. Note that if
multiple files have the same upload timestamp as either the first or last file, all will be processed.</p>
<p>First file in range <input type="text" name="first" size="60"><br>
Last file in range <input type="text" name="last" size="60"></p>
<p>If filters are specified, files will only be processed if the text appears as a substring in either the wiki
source of the author field, or in the camera model in Exif. The matching is case insensitive. Only the first
line of the author field is examined.</p>
<p>Author filter <input type="text" name="author" size="50"><br>
Camera model filter <input type="text" name="model" size="50"></p>
<p>After pressing Submit, it may take some time before output appears. Edits are limited to one per five seconds,
and can be examined in real-time at your contributions page at Commons. If you need to stop the tool, press the
browser stop button, close the page, or revoke OAuth access at
`)
	writeLink(w, oauthManageURL, "Special:OAuthManageMyGrants")
	writeString(w, `</p>
<input type="submit" value="Submit">
</form></body></html>`)
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
		return noinfo, errors.New("File not found.")
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
		return noinfo, noinfo, errors.New("Empty pages array when requesting imageinfo.")
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
				return errors.New("author didn't match.")
			}
		}
		dateStr := fmt.Sprintf("{{DTZ|%s}}", newDate.Format("2006-01-02T15:04:05-07"))
		newText := text[:dateStart] + dateStr + text[dateEnd:]
		if newText == text {
			return errors.New("no change needed.")
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
	writeLink(w, commonsWiki+url.PathEscape(title), title)
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
		return nil, nil
	}
	num, err := strconv.Atoi(param)
	if err == nil {
		hours := num / 100
		mins := num % 100
		return time.FixedZone(param, (hours*60+mins)*60), nil
	}
	if strings.Index(param, "/") == -1 {
		return nil, errors.New("Timezone should be either numeric or a tz database zone name with a slash.")
	}
	return time.LoadLocation(param)
}

func fileParam(param string) (string, error) {
	filePrefix := "File:"
	badChars := "/|"
	param = strings.TrimPrefix(param, commonsWiki)
	if strings.ContainsAny(param, badChars) {
		return "", errors.New("Filenames may not contain the characters " + badChars)
	}
	if len(param) == 0 {
		return param, nil
	}
	if !strings.HasPrefix(param, filePrefix) {
		param = filePrefix + param
	}
	return param, nil
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
	accessToken, err := r.Cookie(tokenCookie)
	if err != nil {
		preMessage(w, title, "Cookie "+tokenCookie+" not set for OAuth.")
		return
	}
	accessSecret, err := r.Cookie(secretCookie)
	if err != nil {
		preMessage(w, title, "Cookie "+secretCookie+" not set for OAuth.")
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
	if cameraZone == nil {
		cameraZone = localZone
	}
	if localZone == nil {
		localZone = cameraZone
	}
	if cameraZone == nil {
		preMessage(w, title, "Please supply at least one time zone.")
		return
	}
	first, err := fileParam(trimmedField("first", r))
	if err != nil {
		preError(w, title, err)
		return
	}
	last, err := fileParam(trimmedField("last", r))
	if err != nil {
		preError(w, title, err)
		return
	}
	if first == "" {
		first = last
	}
	if first == "" {
		preMessage(w, title, "Please supply at least one file name.")
		return
	}
	if last == "" {
		last = first
	}
	authorFilter := strings.ToLower(trimmedField("author", r))
	modelFilter := strings.ToLower(trimmedField("model", r))
	client, err := mwclient.New(commonsPrefix+"w/api.php", "dtz; User:Ghouston")
	if err != nil {
		preError(w, title, err)
		return
	}
	client.Maxlag.On = true
	userName, err := authClient(client, accessToken.Value, accessSecret.Value)
	if err != nil {
		preError(w, title, err)
		return
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
		preMessage(w, title, "Two files must be uploaded by the same user.")
		return
	}
	writeHead(w, title)
	writeString(w, "<body>\n")
	writeString(w, "<p>Editing as user ")
	writeString(w, userName)
	writeString(w, "</p>")
	processRange(imageInfo1.uploadTime, imageInfo2.uploadTime, imageInfo1.user, cameraZone, localZone, authorFilter, modelFilter, client, w)
}

func loadPrivateKey() (*rsa.PrivateKey, error) {
	keyFile := os.Getenv("PrivateKeyFile")
	if keyFile == "" {
		return nil, errors.New("PrivateKeyFile not set in environment.")
	}
	bytes, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(bytes)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("Failed to parse PEM private key.")
	}
	pk, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	pkey, ok := pk.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("Failed to typecast private key.")
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
		preMessage(w, title, "OAuth consumer token not set in environment.")
		return
	}
	consumer := oauth.NewRSAConsumer(
		consumerToken,
		privateKey,
		oauth.ServiceProvider{RequestTokenUrl: oauthRequestURL, AuthorizeTokenUrl: oauthAuthorizeURL, AccessTokenUrl: oauthAccessURL})
	/*requestToken*/ _, url, err := consumer.GetRequestTokenAndUrl("oob")
	if err != nil {
		preError(w, title, err)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: tokenCookie, Path: toolRelative, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: secretCookie, Path: toolRelative, MaxAge: -1})
	http.Redirect(w, r, toolRelative, http.StatusSeeOther)
}

func authClient(client *mwclient.Client, oauthToken, oauthSecret string) (string, error) {
	consumerToken := os.Getenv("ConsumerToken")
	if consumerToken == "" {
		return "", errors.New("OAuth consumer token not set in environment.")
	}
	consumer := oauth.NewRSAConsumer(consumerToken, privateKey, oauth.ServiceProvider{RequestTokenUrl: oauthRequestURL, AuthorizeTokenUrl: oauthAuthorizeURL, AccessTokenUrl: oauthAccessURL})
	httpc, err := consumer.MakeHttpClient(&oauth.AccessToken{Token: oauthToken, Secret: oauthSecret})
	if err != nil {
		return "", err
	}
	userName, err := checkUser(httpc)
	if err != nil {
		return "", err
	}
	client.SetHTTPClient(httpc)
	return userName, nil
}

func authGetAccess(token, verifier string) (string, string, error) {
	consumerToken := os.Getenv("ConsumerToken")
	if consumerToken == "" {
		return "", "", errors.New("OAuth consumer token not set in environment.")
	}
	consumer := oauth.NewRSAConsumer(consumerToken, privateKey, oauth.ServiceProvider{RequestTokenUrl: oauthRequestURL, AuthorizeTokenUrl: oauthAuthorizeURL, AccessTokenUrl: oauthAccessURL})
	access, err := consumer.AuthorizeToken(&oauth.RequestToken{Token: token}, verifier)
	if err != nil {
		return "", "", err
	}
	return access.Token, access.Secret, nil
}

func checkUser(client *http.Client) (string, error) {
	resp, err := client.Get(commonsWiki + "Special:OAuth/identify")
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", err
	}
	consumerSecret := os.Getenv("ConsumerSecret")
	if consumerSecret == "" {
		return "", errors.New("OAuth consumer secret not set in environment.")
	}
	token, err := jwt.Parse(string(body), func(token *jwt.Token) (interface{}, error) { return []byte(consumerSecret), nil })
	if err != nil {
		return "", err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", errors.New("Claims not a map?")
	}
	blocked, ok := claims["blocked"].(bool)
	if !ok {
		return "", errors.New("Claims.blocked is not bool")
	}
	groups, ok := claims["groups"].([]interface{})
	if !ok {
		return "", errors.New("Claims.groups is not an array of interfaces")
	}
	autoconfirmed := false
	for i := range groups {
		group, ok := groups[i].(string)
		if !ok {
			return "", errors.New("Claims.groups[i] is not a string")
		}
		if group == "autoconfirmed" {
			autoconfirmed = true
		}
	}
	if !autoconfirmed {
		return "", errors.New("User is not autoconfirmed.")
	}
	if blocked {
		return "", errors.New("User is blocked.")
	}
	userName, ok := claims["username"].(string)
	if !ok {
		return "", errors.New("Claims.username is not a string")
	}
	return userName, nil
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		fmt.Println("PORT not set in environment")
		return
	}
	var err error
	if privateKey, err = loadPrivateKey(); err != nil {
		fmt.Println(err)
		return
	}
	http.HandleFunc("/", rootHandler)
	http.HandleFunc(outputRelative, outputHandler)
	http.HandleFunc(authRelative, authHandler)
	http.HandleFunc(logoutRelative, logoutHandler)

	if err = http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Println(err)
		return
	}
}
