// translations downloads translations, uploads translations, prints summary
// for translations, prints unused strings.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AdguardTeam/AdGuardHome/internal/aghio"
	"github.com/AdguardTeam/golibs/errors"
	"github.com/AdguardTeam/golibs/log"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

const (
	twoskyConfFile   = "./.twosky.json"
	localesDir       = "./client/src/__locales"
	defaultBaseFile  = "en.json"
	defaultProjectID = "home"
	srcDir           = "./client/src"
	twoskyURI        = "https://twosky.int.agrd.dev/api/v1"

	readLimit = 1 * 1024 * 1024
)

// langCode is a language code.
type langCode string

// languages is a map, where key is language code and value is display name.
type languages map[langCode]string

// textlabel is a text label of localization.
type textLabel string

// locales is a map, where key is text label and value is translation.
type locales map[textLabel]string

func main() {
	if len(os.Args) == 1 {
		usage("need a command")
	}

	if os.Args[1] == "help" {
		usage("")
	}

	uriStr := os.Getenv("TWOSKY_URI")
	if uriStr == "" {
		uriStr = twoskyURI
	}

	uri, err := url.Parse(uriStr)
	check(err)

	projectID := os.Getenv("TWOSKY_PROJECT_ID")
	if projectID == "" {
		projectID = defaultProjectID
	}

	conf, err := readTwoskyConf()
	check(err)

	command(uri, projectID, conf)
}

func command(uri *url.URL, projectID string, conf twoskyConf) {
	switch os.Args[1] {
	case "summary":
		err := summary(conf.Languages)
		check(err)
	case "download":
		err := download(uri, projectID, conf.Languages)
		check(err)
	case "unused":
		err := unused()
		check(err)
	case "upload":
		err := upload(uri, projectID, conf.BaseLangcode)
		check(err)
	// TODO!!: naming
	case "update":
		err := update(uri, projectID, conf.Languages, conf.BaseLangcode)
		check(err)
	case "upgrade":
		err := upgrade(uri, projectID, conf.Languages, conf.BaseLangcode)
		check(err)
	default:
		usage("unknown command")
	}
}

// check is a simple error-checking helper for scripts.
func check(err error) {
	if err != nil {
		panic(err)
	}
}

// usage prints usage.  If addStr is not empty print addStr and exit with code
// 1, otherwise exit with code 0.
func usage(addStr string) {
	const usageStr = `Usage: go run main.go <command> [<args>]
Commands:
  help
        Print usage.
  summary
        Print summary.
  download [-n <count>]
        Download translations. count is a number of concurrent downloads.
  unused
        Print unused strings.
  upload
        Upload translations.`

	if addStr != "" {
		fmt.Printf("%s\n%s\n", addStr, usageStr)

		os.Exit(1)
	}

	fmt.Println(usageStr)

	os.Exit(0)
}

// twoskyConf is the configuration structure for localization.
type twoskyConf struct {
	Languages        languages `json:"languages"`
	ProjectID        string    `json:"project_id"`
	BaseLangcode     langCode  `json:"base_locale"`
	LocalizableFiles []string  `json:"localizable_files"`
}

// readTwoskyConf returns configuration.
func readTwoskyConf() (t twoskyConf, err error) {
	b, err := os.ReadFile(twoskyConfFile)
	if err != nil {
		// Don't wrap the error since it's informative enough as is.
		return twoskyConf{}, err
	}

	var tsc []twoskyConf
	err = json.Unmarshal(b, &tsc)
	if err != nil {
		err = fmt.Errorf("unmarshalling %q: %w", twoskyConfFile, err)

		return twoskyConf{}, err
	}

	if len(tsc) == 0 {
		err = fmt.Errorf("%q is empty", twoskyConfFile)

		return twoskyConf{}, err
	}

	conf := tsc[0]

	for _, lang := range conf.Languages {
		if lang == "" {
			return twoskyConf{}, errors.Error("language is empty")
		}
	}

	return conf, nil
}

// readLocales reads file with name fn and returns a map, where key is text
// label and value is localization.
func readLocales(fn string) (loc locales, err error) {
	b, err := os.ReadFile(fn)
	if err != nil {
		// Don't wrap the error since it's informative enough as is.
		return nil, err
	}

	loc = make(locales)
	err = json.Unmarshal(b, &loc)
	if err != nil {
		err = fmt.Errorf("unmarshalling %q: %w", fn, err)

		return nil, err
	}

	return loc, nil
}

// summary prints summary for translations.
func summary(langs languages) (err error) {
	sum, err := getSummary(langs)
	if err != nil {
		return fmt.Errorf("summary: %w", err)
	}

	for lang, f := range sum {
		fmt.Printf("%s\t %6.2f %%\n", lang, f)
	}

	return nil
}

func getSummary(langs languages) (sum map[langCode]float64, err error) {
	sum = make(map[langCode]float64)

	basePath := filepath.Join(localesDir, defaultBaseFile)
	baseLoc, err := readLocales(basePath)
	if err != nil {
		return nil, fmt.Errorf("reading locales: %w", err)
	}

	size := float64(len(baseLoc))

	keys := maps.Keys(langs)
	slices.Sort(keys)

	for _, lang := range keys {
		name := filepath.Join(localesDir, string(lang)+".json")
		if name == basePath {
			continue
		}

		var loc locales
		loc, err = readLocales(name)
		if err != nil {
			return nil, fmt.Errorf("summary: reading locales: %w", err)
		}

		sum[lang] = float64(len(loc)) * 100 / size
	}

	return sum, nil
}

// download and save all translations.  uri is the base URL.  projectID is the
// name of the project.
func download(uri *url.URL, projectID string, langs languages) (err error) {
	var numWorker int

	flagSet := flag.NewFlagSet("download", flag.ExitOnError)
	flagSet.Usage = func() {
		usage("download command error")
	}
	flagSet.IntVar(&numWorker, "n", 1, "number of concurrent downloads")

	err = flagSet.Parse(os.Args[2:])
	if err != nil {
		// Don't wrap the error since there is exit on error.
		return err
	}

	if numWorker < 1 {
		usage("count must be positive")
	}

	downloadURI := uri.JoinPath("download")

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	wg := &sync.WaitGroup{}
	uriCh := make(chan *url.URL, len(langs))

	for i := 0; i < numWorker; i++ {
		wg.Add(1)
		go downloadWorker(wg, client, uriCh)
	}

	for lang := range langs {
		uri = translationURL(downloadURI, defaultBaseFile, projectID, lang)

		uriCh <- uri
	}

	close(uriCh)
	wg.Wait()

	return nil
}

// downloadWorker downloads translations by received urls and saves them.
func downloadWorker(wg *sync.WaitGroup, client *http.Client, uriCh <-chan *url.URL) {
	defer wg.Done()

	for uri := range uriCh {
		data, err := getTranslation(client, uri.String())
		if err != nil {
			log.Error("download worker: getting translation: %s", err)

			continue
		}

		q := uri.Query()
		code := q.Get("language")

		name := filepath.Join(localesDir, code+".json")
		err = os.WriteFile(name, data, 0o664)
		if err != nil {
			log.Error("download worker: writing file: %s", err)

			continue
		}

		fmt.Println(name)
	}
}

// getTranslation returns received translation data or error.
func getTranslation(client *http.Client, url string) (data []byte, err error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("requesting: %w", err)
	}

	defer log.OnCloserError(resp.Body, log.ERROR)

	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("url: %q; status code: %s", url, http.StatusText(resp.StatusCode))

		return nil, err
	}

	limitReader, err := aghio.LimitReader(resp.Body, readLimit)
	if err != nil {
		err = fmt.Errorf("limit reading: %w", err)

		return nil, err
	}

	data, err = io.ReadAll(limitReader)
	if err != nil {
		err = fmt.Errorf("reading all: %w", err)

		return nil, err
	}

	return data, nil
}

// translationURL returns a new url.URL with provided query parameters.
func translationURL(oldURL *url.URL, baseFile, projectID string, lang langCode) (uri *url.URL) {
	uri = &url.URL{}
	*uri = *oldURL

	q := uri.Query()
	q.Set("format", "json")
	q.Set("filename", baseFile)
	q.Set("project", projectID)
	q.Set("language", string(lang))

	uri.RawQuery = q.Encode()

	return uri
}

// unused prints unused text labels.
func unused() (err error) {
	fileNames := []string{}
	basePath := filepath.Join(localesDir, defaultBaseFile)
	baseLoc, err := readLocales(basePath)
	if err != nil {
		return fmt.Errorf("unused: %w", err)
	}

	locDir := filepath.Clean(localesDir)

	err = filepath.Walk(srcDir, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			log.Info("warning: accessing a path %q: %s", name, err)

			return nil
		}

		if info.IsDir() {
			return nil
		}

		if strings.HasPrefix(name, locDir) {
			return nil
		}

		ext := filepath.Ext(name)
		if ext == ".js" || ext == ".json" {
			fileNames = append(fileNames, name)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("filepath walking %q: %w", srcDir, err)
	}

	err = removeUnused(fileNames, baseLoc)

	return errors.Annotate(err, "removing unused: %w")
}

func removeUnused(fileNames []string, loc locales) (err error) {
	knownUsed := []textLabel{
		"blocking_mode_refused",
		"blocking_mode_nxdomain",
		"blocking_mode_custom_ip",
	}

	for _, v := range knownUsed {
		delete(loc, v)
	}

	for _, fn := range fileNames {
		var buf []byte
		buf, err = os.ReadFile(fn)
		if err != nil {
			// Don't wrap the error since it's informative enough as is.
			return err
		}

		for k := range loc {
			if bytes.Contains(buf, []byte(k)) {
				delete(loc, k)
			}
		}
	}

	printUnused(loc)

	return nil
}

// printUnused text labels to stdout.
func printUnused(loc locales) {
	keys := maps.Keys(loc)
	slices.Sort(keys)

	for _, v := range keys {
		fmt.Println(v)
	}
}

// upload base translation.  uri is the base URL.  projectID is the name of the
// project.  baseLang is the base language code.
func upload(uri *url.URL, projectID string, baseLang langCode) (err error) {
	uploadURI := uri.JoinPath("upload")

	lang := baseLang

	langStr := os.Getenv("UPLOAD_LANGUAGE")
	if langStr != "" {
		lang = langCode(langStr)
	}

	basePath := filepath.Join(localesDir, defaultBaseFile)
	b, err := os.ReadFile(basePath)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	var buf bytes.Buffer
	buf.Write(b)

	uri = translationURL(uploadURI, defaultBaseFile, projectID, lang)

	var client http.Client
	resp, err := client.Post(uri.String(), "application/json", &buf)
	if err != nil {
		return fmt.Errorf("upload: client post: %w", err)
	}

	defer func() {
		err = errors.WithDeferred(err, resp.Body.Close())
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status code is not ok: %q", http.StatusText(resp.StatusCode))
	}

	return nil
}

// TODO!!: docs
func update(uri *url.URL, projectID string, langs languages, baseLang langCode) (err error) {
	defer func() {
		err = errors.Annotate(err, "update: %w")
	}()

	fmt.Println("Downloading the locales.")

	err = download(uri, projectID, langs)
	if err != nil {
		return err
	}

	fmt.Println("Checking if the English locale is up to date.")

	ch, err := checkBaseLocale()
	if err != nil {
		return err
	}

	if ch == deletion {
		err = handleDeletion(uri, projectID, baseLang)
		if err != nil {
			return err
		}
	}

	if ch == addition {
		err = handleAddition()
		if err != nil {
			return err
		}
	}

	fmt.Println("Checking if the release-blocker locales are fully translated")

	err = checkReleaseBlockerLocales(langs)
	if err != nil {
		return err
	}

	fmt.Println("Adding non-deletion changes.")

	err = addAdditions()
	if err != nil {
		return err
	}

	if !ask("Proceed to remove deletion changes?") {
		return nil
	}

	fmt.Println("Removing deletion changes.")

	err = restoreLocales()
	check(err)

	fmt.Println(`Make sure that you're on fresh master.
    git status
Commit the changes.
    git checkout -b 'upd-i18n'
    git commit -m 'client: upd i18n'
When merging the commit, only mention the GitHub issue 2643 if
the update is before a final release as opposed to a beta one.`)

	return nil
}

func handleDeletion(uri *url.URL, projectID string, baseLang langCode) (err error) {
	err = restoreLocales()
	if err != nil {
		return err
	}

	if !ask("Upload the English locale?") {
		return nil
	}

	err = upload(uri, projectID, baseLang)
	if err != nil {
		return err
	}

	fmt.Println("Make sure that the new text has appeared on Crowdin.")

	return nil
}

func handleAddition() (err error) {
	// If there are additions, then someone probably unuploaded changes
	// from unmerged branches.  This isn't critical, so just add this
	// change.
	fmt.Println("Adding english locale.")
	err = addBaseLocale()

	if err != nil {
		return err
	}

	return nil
}

type change int

const (
	wrong change = iota - 2
	deletion
	none
	addition
)

// checkBaseLocale returns change or error.  change is 0 if there is no
// changes, 1 if there is additions, and -1 if there is deletions.
func checkBaseLocale() (ch change, err error) {
	path := filepath.Join(localesDir, defaultBaseFile)
	cmd := exec.Command("git", "diff", "-U0", path)

	buf := &bytes.Buffer{}
	cmd.Stdout = buf

	err = cmd.Run()
	if err != nil {
		return wrong, fmt.Errorf("checking base locale: %w", err)
	}

	if buf.Len() == 0 {
		return none, nil
	}

	scanner := bufio.NewScanner(buf)

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "+++") {
			// continue
		} else if strings.HasPrefix(line, "+") {
			return addition, nil
		}
	}

	return deletion, nil
}

func checkReleaseBlockerLocales(langs languages) (err error) {
	sum, err := getSummary(langs)
	if err != nil {
		return fmt.Errorf("checking release blocker locales: %w", err)
	}

	blockers := []langCode{
		"de",
		"es",
		"fr",
		"it",
		"ja",
		"ko",
		"pt-br",
		"pt-pt",
		"ru",
		"zh-cn",
		"zh-tw",
	}

	for _, b := range blockers {
		if sum[b] != 100 {
			return fmt.Errorf("locale %q is not fully translated", b)
		}
	}

	return nil
}

func restoreLocales() (err error) {
	cmd := exec.Command("git", "restore", localesDir)

	buf := &bytes.Buffer{}
	cmd.Stdout = buf

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("checking base locale: %w", err)
	}

	return nil
}

func addBaseLocale() (err error) {
	path := filepath.Join(localesDir, defaultBaseFile)
	cmd := exec.Command("git", "add", "-p", path)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("adding base locale: %w", err)
	}

	fmt.Printf("adding base locale: %q\n", path)

	return nil
}

func addAdditions() (err error) {
	adds, err := getAdditions()
	if err != nil {
		return fmt.Errorf("adding changes: %w", err)
	}

	for _, v := range adds {
		path := filepath.Join(localesDir, v)

		cmd := exec.Command("git", "add", "-p", path)

		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		err = cmd.Run()
		if err != nil {
			return fmt.Errorf("adding locale %q: %w", path, err)
		}
	}

	return nil
}

func getAdditions() (adds []string, err error) {
	cmd := exec.Command("git", "diff", "-U0", localesDir)

	buf := &bytes.Buffer{}
	cmd.Stdout = buf

	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("update running: %w", err)
	}

	scanner := bufio.NewScanner(buf)
	key := ""

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "+++") {
			i := strings.LastIndex(line, "/")

			key = line[i:]
		} else if strings.HasPrefix(line, "+") {
			adds = append(adds, key)
		}
	}

	return adds, nil
}

func upgrade(uri *url.URL, projectID string, langs languages, baseLang langCode) (err error) {
	fmt.Println(`Do not upload anything until the feature branch that
introduces the text is merged.  Once the branch is merged, proceed.`)

	if !ask("Proceed?") {
		return nil
	}

	defer func() {
		err = errors.Annotate(err, "upgrade: %w")
	}()

	fmt.Println("Downloading the locales.")

	err = download(uri, projectID, langs)
	if err != nil {
		fmt.Println("up grade download")
		return err
	}

	fmt.Println("Checking if the English locale is up to date.")

	ch, err := checkBaseLocale()
	if err != nil {
		return err
	}

	if ch == none {
		fmt.Println("There is no changes.  Exiting.")

		return nil
	} else if ch == addition {
		// If there are additions, then someone probably unuploaded changes
		// from unmerged branches.  This isn't critical, so just add this
		// change.
		fmt.Println("Adding english locale.")
		err = addBaseLocale()

		if err != nil {
			return err
		}
	}

	if !ask("Proceed to restore locales?") {
		return nil
	}

	err = restoreLocales()
	if err != nil {
		return err
	}

	fmt.Println("Uploading english locale.")
	err = upload(uri, projectID, baseLang)
	if err != nil {
		return err
	}

	return nil
}

// ask reads line from STDIN if line is 'y' returns true, otherwise false.
func ask(s string) (a bool) {
	const q = "y/[n]: "

	if s != "" {
		fmt.Printf("%s ", s)
	}
	fmt.Printf("%s ", q)

	scanner := bufio.NewScanner(os.Stdin)

	scanner.Scan()
	line := scanner.Text()
	line = strings.ToLower(line)

	return line == "y"
}
