// Package main implements kindledl
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

const (
	program = "kindledl"
)

// Flags
var (
	debug              = flag.Bool("debug", false, "set to see debug messages")
	login              = flag.Bool("login", false, "set to launch login browser")
	show               = flag.Bool("show", false, "set to show the browser (not headless)")
	booksPerPage       = flag.Int("books-per-page", 25, "Books shown on each page")
	book               = flag.Int("book", 0, "Book to start downloading from")
	output             = flag.String("output", "Books", "directory to store the downloaded books")
	checkpoint         = flag.String("checkpoint", program+"-checkpoint.txt", "File noting where the download has got to, ignored if -book is set")
	kindleName         = flag.String("kindle", "", "Name of the kindle to download for")
	useJSON            = flag.Bool("json", false, "log in JSON format")
	booksURL           = flag.String("books-url", "https://www.amazon.co.uk/hz/mycd/digital-console/contentlist/booksPurchases/dateAsc/", "URL to show purchased kindle books in date order, oldest first")
	msgMoreActions     = flag.String("msg-more-actions", "More actions", "Text to look for to find the more actions button")
	msgDownloadViaUSB  = flag.String("msg-download-usb", "Download & transfer via USB", "Text to look for in more actions menu")
	msgClearFurthest   = flag.String("msg-clear-furthest", "Clear Furthest Page Read", "Text to look for in more actions menu to check it is OK")
	msgDownloadButton  = flag.String("msg-download-button", "Download", "Text to look for to find the download button")
	msgSuccess         = flag.String("msg-success", "Success", "Text to look for in the title of the success popup")
	msgShowing         = flag.String("msg-showing", `Showing.*\s+(\d+)\s+to\s+(\d+)\s+of\s+(\d+)\s+items`, "What books the page is showing")
	timeActionInterval = flag.Duration("time-action-interval", time.Second, "Minimum time between browser actions")
	timeRetrySleep     = flag.Duration("time-retry-sleep", time.Second, "Time to wait between retry of finding something on the page")
	timeScrollPause    = flag.Duration("time-scroll-pause", 500*time.Millisecond, "Time to wait after scrolling the page")
)

// Global variables
var (
	configRoot       string      // top level config dir, typically "~/.config/"+program
	browserConfig    string      // work directory for browser instance
	browserPath      string      // path to the browser binary
	downloadDir      string      // directory for downloads
	browserPrefs     string      // JSON config for the browser
	version          = "DEV"     // set by goreleaser
	commit           = "NONE"    // set by goreleaser
	date             = "UNKNOWN" // set by goreleaser
	reMoreActions    *regexp.Regexp
	reDownloadViaUSB *regexp.Regexp
	reClearFurthest  *regexp.Regexp
	reDownloadButton *regexp.Regexp
	reSuccess        *regexp.Regexp
	reShowing        *regexp.Regexp
	reKindleName     *regexp.Regexp
	errFinished      = errors.New("downloads finished")
)

// Set up the global variables from the flags
func config() (err error) {
	version := fmt.Sprintf("%s version %s, commit %s, built at %s", program, version, commit, date)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n%s\n", version)
	}
	flag.Parse()

	// Set up the logger
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	if *useJSON {
		logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
		slog.SetDefault(logger)
	} else {
		slog.SetLogLoggerLevel(level) // set log level of Default Handler
	}
	slog.Debug(version)

	configRoot, err = os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("didn't find config directory: %w", err)
	}
	configRoot = filepath.Join(configRoot, program)
	browserConfig = filepath.Join(configRoot, "browser")
	err = os.MkdirAll(browserConfig, 0700)
	if err != nil {
		return fmt.Errorf("config directory creation: %w", err)
	}
	slog.Debug("Configured config", "config_root", configRoot, "browser_config", browserConfig)

	downloadDir, err = filepath.Abs(*output)
	if err != nil {
		return fmt.Errorf("download directory absolute path: %w", err)
	}
	err = os.MkdirAll(downloadDir, 0777)
	if err != nil {
		return fmt.Errorf("download directory creation: %w", err)
	}
	slog.Info("Created download directory", "download_directory", downloadDir)

	// Find the browser
	var ok bool
	browserPath, ok = launcher.LookPath()
	if !ok {
		return errors.New("browser not found")
	}
	slog.Debug("Found browser", "browser_path", browserPath)

	// Browser preferences
	pref := map[string]any{
		"download": map[string]any{
			"default_directory": downloadDir,
		},
	}
	prefJSON, err := json.Marshal(pref)
	if err != nil {
		return fmt.Errorf("failed to make preferences: %w", err)
	}
	browserPrefs = string(prefJSON)
	slog.Debug("made browser preferences", "prefs", browserPrefs)

	// Compile regexps from messages
	for _, msg := range []struct {
		re  **regexp.Regexp
		txt *string
	}{
		{&reMoreActions, msgMoreActions},
		{&reDownloadViaUSB, msgDownloadViaUSB},
		{&reClearFurthest, msgClearFurthest},
		{&reDownloadButton, msgDownloadButton},
		{&reSuccess, msgSuccess},
		{&reShowing, msgShowing},
		{&reKindleName, kindleName},
	} {
		*msg.re, err = regexp.Compile(`(?i)^\s*` + *msg.txt + `\s*$`)
		if err != nil {
			return fmt.Errorf("failed to compile match string %q as regexp: %w", *msg.txt, err)
		}
	}

	return nil
}

// logger makes an io.Writer from slog.Debug
type logger struct{}

// Write writes len(p) bytes from p to the underlying data stream.
func (logger) Write(p []byte) (n int, err error) {
	s := string(p)
	s = strings.TrimSpace(s)
	slog.Debug(s)
	return len(p), nil
}

// Println is called to log text
func (logger) Println(vs ...any) {
	s := fmt.Sprint(vs...)
	s = strings.TrimSpace(s)
	slog.Debug(s)
}

// Kindle is a single page browser for Amazon Books
type Kindle struct {
	browser    *rod.Browser
	page       *rod.Page
	book       int // current book we are downloading
	pageNumber int // page number we are looking at
	offset     int // current offset
	totalBooks int // total number of books to download
}

// New creates a new browser on the books main page to check we are logged in
func New() (*Kindle, error) {
	k := &Kindle{
		book:       1,
		totalBooks: -1,
	}
	err := k.startBrowser()
	if err != nil {
		return nil, err
	}
	// Work out where we are starting from
	if *book > 0 {
		k.book = *book
	} else {
		err = k.loadCheckpoint()
		if err != nil {
			return nil, err
		}
	}
	// k.page and k.pageNumber are 1 based
	// k.offset is 0 based
	k.pageNumber = (k.book-1) / *booksPerPage + 1
	k.offset = (k.book - 1) % *booksPerPage
	slog.Info("Starting downloads", "book", k.book)
	return k, nil
}

// loadCheckpoint loads the current book position from the checkpoint file
func (k *Kindle) loadCheckpoint() error {
	data, err := os.ReadFile(*checkpoint)
	if os.IsNotExist(err) {
		k.book = 1
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to read checkpoint file %q: %w", *checkpoint, err)
	}
	book, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("failed to convert checkpoint file content to integer: %w", err)
	}
	k.book = book
	return nil
}

// saveCheckpoint saves the current book position to the checkpoint file
func (k *Kindle) saveCheckpoint() error {
	data := []byte(strconv.Itoa(k.book))
	err := os.WriteFile(*checkpoint, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write checkpoint file %q: %w", *checkpoint, err)
	}
	return nil
}

// Returns the URL for the current page number
func (k *Kindle) pageURL() string {
	return fmt.Sprintf("%s?pageNumber=%d", *booksURL, k.pageNumber)
}

// start the browser off and check it is authenticated
func (k *Kindle) startBrowser() error {
	// We use the default profile in our new data directory
	l := launcher.New().
		Bin(browserPath).
		Headless(!*show).
		UserDataDir(browserConfig).
		Preferences(browserPrefs).
		Set("disable-gpu").
		Set("disable-audio-output").
		Logger(logger{})

	url, err := l.Launch()
	if err != nil {
		return fmt.Errorf("browser launch: %w", err)
	}

	k.browser = rod.New().
		ControlURL(url).
		NoDefaultDevice().
		Trace(true).
		SlowMotion(*timeActionInterval).
		Logger(logger{})

	err = k.browser.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to browser: %w", err)
	}

	k.page, err = k.browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return fmt.Errorf("failed to open new browser page: %w", err)
	}
	return nil
}

// Opens the current page with 25 books on
func (k *Kindle) openPage() (err error) {
	url := k.pageURL()
	err = k.page.Navigate(url)
	if err != nil {
		return fmt.Errorf("couldn't open books URL %q: %w", url, err)
	}

	eventCallback := func(e *proto.PageLifecycleEvent) {
		slog.Debug("Event", "Name", e.Name, "Dump", e)
	}
	k.page.EachEvent(eventCallback)

	err = k.page.WaitLoad()
	if err != nil {
		return fmt.Errorf("books page load: %w", err)
	}

	authenticated := false
	for try := 0; try < 60; try++ {
		time.Sleep(*timeRetrySleep)
		info := k.page.MustInfo()
		slog.Debug("URL", "url", info.URL)
		// When not authenticated Amazon redirects away from the Books URL
		if info.URL == url {
			authenticated = true
			slog.Debug("Authenticated")
			break
		}
		// However if we select beyond the end, then we get redirected back to a previous page
		if strings.HasPrefix(info.URL, *booksURL) {
			return errFinished
		}
		slog.Info("Please log in, or re-run with -login flag")
	}
	if !authenticated {
		return errors.New("browser is not logged in - rerun with the -login flag")
	}
	return nil
}

// Find the elements of type with the text
func (k *Kindle) findElementWithText(subLog *slog.Logger, elementName string, match *regexp.Regexp) (found rod.Elements, err error) {
	subLog = subLog.With(
		"elementName", elementName,
		"text", match.String(),
	)
	for i := 0; i < 5; i++ {
		subLog.Debug("Looking for element with text", "try", i)
		elements, err := k.page.Elements(elementName)
		if err != nil {
			return nil, fmt.Errorf("error looking for %q with %q on page: %w", elementName, match, err)
		}
		for _, el := range elements {
			elText, err := el.Text()
			if err != nil {
				return nil, fmt.Errorf("error looking for %q with %q in span: %w", elementName, match, err)
			}
			if match.MatchString(elText) {
				found = append(found, el)
			}
		}
		if len(found) > 0 {
			break
		}
		time.Sleep(*timeRetrySleep)
	}
	return found, nil
}

var errNoneFound = errors.New("none found")

// As findOneElementWithText but returns only one
func (k *Kindle) findOneElementWithText(subLog *slog.Logger, elementName string, match *regexp.Regexp) (el *rod.Element, err error) {
	found, err := k.findElementWithText(subLog, elementName, match)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no %q containing %q found: %w", elementName, match, errNoneFound)
	} else if len(found) != 1 {
		return nil, fmt.Errorf("expecting 1 %q containing %q but found %d", elementName, match, len(found))
	}
	return found[0], err
}

// Download the n-th book with the menu passed in
func (k *Kindle) downloadOneBook(subLog *slog.Logger, n int, action *rod.Element) error {
	subLog = subLog.With(
		"book", k.book,
		"book_number", n+1,
	)

	err := action.ScrollIntoView()
	if err != nil {
		return fmt.Errorf("error scrolling button into view: %w", err)
	}

	// Small pause to let things settle
	time.Sleep(*timeScrollPause)

	subLog.Debug("Opening more actions menu")
	err = action.Click(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return fmt.Errorf("error clicking on more actions: %w", err)
	}

	// Check the menu exists
	clearFurthest, err := k.findOneElementWithText(subLog, "span", reClearFurthest)
	if err != nil {
		return fmt.Errorf("couldn't find popup menu (-msg-clear-furthest=%q): %w", *msgClearFurthest, err)
	}

	// ... as some books (eg SAMPLES) don't have a download link
	menu, err := k.findOneElementWithText(subLog, "span", reDownloadViaUSB)
	if errors.Is(err, errNoneFound) {
		slog.Error(fmt.Sprintf("Book has no (-msg-download-usb=%q) link - skipping", *msgDownloadViaUSB))

		// Get the element's position
		shape, err := clearFurthest.Shape()
		if err != nil {
			return fmt.Errorf("failed to get shape to dismiss popup: %w", err)
		}

		// Click a bit off the side of the box to dismiss it
		x := shape.Box().X - 50
		y := shape.Box().Y

		// Move mouse to the new coordinates and click to dismiss the box
		err = k.page.Mouse.MoveTo(proto.Point{X: x, Y: y})
		if err != nil {
			return fmt.Errorf("failed to move mouse to dismiss popup: %w", err)
		}
		err = k.page.Mouse.Click(proto.InputMouseButtonLeft, 1)
		if err != nil {
			return fmt.Errorf("failed to click mouse to dismiss popup: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("couldn't find popup menu (-msg-download-usb=%q): %w", *msgDownloadViaUSB, err)
	}

	subLog.Debug("Opening download menu")
	err = menu.Click(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return fmt.Errorf("error clicking on Download & transfer via USB button: %w", err)
	}

	// Choose kindle popup
	_ = `
<li class="ActionList-module_action_list_item__LoNyc">
  <div style="width: 20px;">
    <label class="RadioButton-module_radio_container__3ni_P">
      <input type="radio" name="actionListRadioButton">
      <span id="download_and_transfer_list_B000JMLBHU_3" class="RadioButton-module_radio__1k8O3" tabindex="0">
      </span>
    </label>
  </div>
  <div class="ActionList-module_action_list_value__ijMh2">
    Nick's Paperwhite Kindle
  </div>
</li>
`

	kindle, err := k.findOneElementWithText(subLog, "li div", reKindleName)
	if err != nil {
		return fmt.Errorf("couldn't find kindle name in menu (-kindle=%q): %w", *kindleName, err)
	}

	li, err := kindle.Parent()
	if err != nil {
		return fmt.Errorf("couldn't find li parent of kindle: %w", err)
	}

	input, err := li.Element("input[type='radio']")
	if err != nil {
		return fmt.Errorf("couldn't find radio in kindle menu: %w", err)
	}

	subLog.Debug("Selecting desired kindle")
	err = input.Click(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return fmt.Errorf("error clicking on selected kindle: %w", err)
	}

	downloadButton, err := k.findOneElementWithText(subLog, "span", reDownloadButton)
	if err != nil {
		return fmt.Errorf("couldn't find download button (-msg-download-button=%q): %w", *msgDownloadButton, err)
	}

	subLog.Debug("Downloading book")
	err = downloadButton.Click(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return fmt.Errorf("error clicking on download button: %w", err)
	}

	// Success popup
	_ = `
<div id="notification-success" class="Notification-module_message_container__1I59M">
  <div class="Notification-module_message_wrapper__1KMgj Notification-module_message_wrapper_success__2RUp8">
    <span id="notification-close" class="Notification-module_close__2N_IB" tabindex="0">
    </span>
    <div class="Notification-module_message_heading__2vO83 Notification-module_message_heading_success__1rCJl">
      <i aria-hidden="true" class="fa fa-check">
      </i>
      <div class="Notification-module_message_heading_container_success__zVMaH">
        <span>Success</span>
      </div>
    </div>
    <div id="success_d0" class="Notification-module_message_heading_container__2R3WZ">
      <span>Download your Kindle content to your computer via Your Media Library.</span>
    </div>
  </div>
</div>
`
	success, err := k.findOneElementWithText(subLog, "span", reSuccess)
	if err != nil {
		return fmt.Errorf("couldn't find success popup (-msg-success=%q): %w", *msgSuccess, err)
	}

	successDiv, err := success.Parent()
	if err != nil {
		return fmt.Errorf("couldn't find div parent of success: %w", err)
	}

	successDivDiv, err := successDiv.Parent()
	if err != nil {
		return fmt.Errorf("couldn't find div div parent of success: %w", err)
	}

	successDivDivDiv, err := successDivDiv.Parent()
	if err != nil {
		return fmt.Errorf("couldn't find div div div parent of success: %w", err)
	}

	close, err := successDivDivDiv.Element("span")
	if err != nil {
		return fmt.Errorf("success close box: %w", err)
	}

	// Click in the close box to make it go away
	err = close.Click(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return fmt.Errorf("error clicking on success popup: %w", err)
	}

	subLog.Info("Downloaded book")
	return nil
}

// Download all the books on the given page
func (k *Kindle) downloadAllOnPage() error {
	err := k.openPage()
	if err != nil {
		return err
	}

	subLog := slog.Default().With(
		"url", k.pageURL(),
		"page", k.pageNumber,
	)

	// Find out how many books on this page
	showing, err := k.findOneElementWithText(subLog, "span", reShowing)
	if err != nil {
		return fmt.Errorf("couldn't find showing text (-msg-showing=%q): %w", *msgShowing, err)
	}
	showingTxt, err := showing.Text()
	if err != nil {
		return fmt.Errorf("couldn't get showing text (-msg-showing=%q): %w", *msgShowing, err)
	}
	match := reShowing.FindStringSubmatch(showingTxt)
	if len(match) != 4 {
		return fmt.Errorf("showing text regexp didn't match (-msg-showing=%q): %w", *msgShowing, err)
	}
	startBook, _ := strconv.Atoi(match[1])
	endBook, _ := strconv.Atoi(match[2])
	totalBooks, _ := strconv.Atoi(match[3])
	slog.Info("Opened new page", "startBook", startBook, "endBook", endBook, "totalBooks", totalBooks)
	k.totalBooks = totalBooks

	// Find all the spans with text "More actions"
	// Each of these is a book
	actions, err := k.findElementWithText(subLog, "span", reMoreActions)
	if err != nil {
		return fmt.Errorf("couldn't find books (-msg-more-actions=%q): %w", *msgMoreActions, err)
	}
	subLog.Debug("Found in page", "books", len(actions))
	if len(actions) == 0 {
		return fmt.Errorf("no books found on page")
	}

	for n, action := range actions {
		if n < k.offset {
			subLog.Debug("skip offset", "offset", n)
			continue
		}
		err = k.downloadOneBook(subLog, n, action)
		if err != nil {
			return err
		}
		k.book++
		err = k.saveCheckpoint()
		if err != nil {
			return err
		}
	}
	k.offset = 0

	return nil
}

// Close the browser
func (k *Kindle) Close() {
	err := k.browser.Close()
	if err == nil {
		slog.Debug("Closed browser")
	} else {
		slog.Error("Failed to close browser", "err", err)
	}
}

// Log the browser in
func doLogin() error {
	slog.Info("Log in to amazon with the browser that pops up, close it, then re-run this without the -login flag")
	cmd := exec.Command(browserPath, "--user-data-dir="+browserConfig, *booksURL)
	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start browser: %w", err)
	}
	slog.Info("Waiting for browser to be closed")
	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("browser run failed: %w", err)
	}
	slog.Info("Now restart this program without -login")
	return nil
}

// Run the downloader returning an error if needed
func run() error {
	err := config()
	if err != nil {
		return err
	}

	// If login is required, run the browser standalone
	if *login {
		return doLogin()
	}

	if *kindleName == "" {
		return fmt.Errorf(`need name of kindle, add something like -kindle "My Kindle"`)
	}

	k, err := New()
	if err != nil {
		return err
	}
	defer k.Close()

	for {
		err = k.downloadAllOnPage()
		if err != nil {
			return err
		}
		k.pageNumber++
		if k.book > k.totalBooks {
			return errFinished
		}
	}
}

func main() {
	err := run()
	if errors.Is(err, errFinished) {
		slog.Info(err.Error())
		err = nil
	}
	if err != nil {
		slog.Error(err.Error())
		os.Exit(2)
	}
}
