package main

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/mitchellh/go-homedir"
	"github.com/skratchdot/open-golang/open"
	cli "github.com/urfave/cli/v2"
)

const (
	appName             = "procon-gardener"
	submissionsEndpoint = "https://kenkoooo.com/atcoder/atcoder-api/v3/user/submissions"
	submissionsPerPage  = 500
)

type AtCoderSubmission struct {
	ID            int     `json:"id"`
	EpochSecond   int64   `json:"epoch_second"`
	ProblemID     string  `json:"problem_id"`
	ContestID     string  `json:"contest_id"`
	UserID        string  `json:"user_id"`
	Language      string  `json:"language"`
	Point         float64 `json:"point"`
	Length        int     `json:"length"`
	Result        string  `json:"result"`
	ExecutionTime int     `json:"execution_time"`
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil && os.IsNotExist(err) {
		return false
	}
	return info.IsDir()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type Service struct {
	RepositoryPath string `json:"repository_path"`
	UserID         string `json:"user_id"`
	UserEmail      string `json:"user_email"`
}

type Config struct {
	Atcoder Service `json:"atcoder"`
}

func languageToFileName(language string) string {
	name := "Main"
	// e.g C++14 (GCC 5.4.1)
	// C++14
	language = strings.Split(language, "(")[0]
	// remove extra last whitespace
	language = strings.TrimSpace(language)

	prefixes := map[string]string{
		"C++":         ".cpp",
		"Bash":        ".sh",
		"Common Lisp": ".lisp",
		"Python":      ".py",
		"PyPy":        ".py",
	}
	for p, ext := range prefixes {
		if strings.HasPrefix(language, p) {
			return name + ext
		}
	}

	names := map[string]string{
		"C":            ".c",
		"C#":           ".cs",
		"Clojure":      ".clj",
		"D":            ".d",
		"Fortran":      ".f08",
		"Go":           ".go",
		"Haskell":      ".hs",
		"JavaScript":   ".js",
		"Java":         ".java",
		"OCaml":        ".ml",
		"Pascal":       ".pas",
		"Perl":         ".pl",
		"PHP":          ".php",
		"Ruby":         ".rb",
		"Scala":        ".scala",
		"Scheme":       ".scm",
		"Main.txt":     ".txt",
		"Visual Basic": ".vb",
		"Objective-C":  ".m",
		"Swift":        ".swift",
		"Rust":         ".rs",
		"Sed":          ".sed",
		"Awk":          ".awk",
		"Brainfuck":    ".bf",
		"Standard ML":  ".sml",
		"Crystal":      ".cr",
		"F#":           ".fs",
		"Unlambda":     ".unl",
		"Lua":          ".lua",
		"LuaJIT":       ".lua",
		"MoonScript":   ".moon",
		"Ceylon":       ".ceylon",
		"Julia":        ".jl",
		"Octave":       ".m",
		"Nim":          ".nim",
		"TypeScript":   ".ts",
		"Perl6":        ".p6",
		"Kotlin":       ".kt",
		"COBOL":        ".cob",
	}
	for n, ext := range names {
		if n == language {
			return name + ext
		}
	}

	log.Printf("Unknown ... %s", language)
	return name + ".txt"
}

func initCmd(strict bool) error {
	log.Println("Initialize your config...")
	home, err := homedir.Dir()
	if err != nil {
		return err
	}
	configDir := filepath.Join(home, "."+appName)
	if !dirExists(configDir) {
		err = os.MkdirAll(configDir, 0o700)
		if err != nil {
			return err
		}
	}

	configFile := filepath.Join(configDir, "config.json")
	if strict || !fileExists(configFile) {
		// initial config
		config := Config{Atcoder: Service{RepositoryPath: "", UserID: ""}}

		jsonBytes, err := json.MarshalIndent(config, "", "\t")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(configDir, "config.json"), jsonBytes, 0o666); err != nil {
			return err
		}
		return nil
	}
	log.Println("Initialized your config at ", configFile)
	return nil
}

func loadConfig() (*Config, error) {
	home, err := homedir.Dir()
	if err != nil {
		return nil, err
	}
	configDir := filepath.Join(home, "."+appName)
	configFile := filepath.Join(configDir, "config.json")
	bytes, err := ioutil.ReadFile(configFile)
	if err != nil {
		return nil, err
	}
	var config Config
	if err = json.Unmarshal(bytes, &config); err != nil {
		log.Println(err)
		return nil, err
	}
	return &config, nil
}

func archiveFile(code, fileName, path string, submission AtCoderSubmission) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(path, fileName), []byte(code), 0o666); err != nil {
		return err
	}
	return nil
}

func submissionsRequest(userID string, fromSecond int64) (*http.Request, error) {
	u, err := url.Parse(submissionsEndpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("user", userID)
	q.Set("from_second", strconv.FormatInt(fromSecond, 10))
	u.RawQuery = q.Encode()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "gzip")
	return req, nil
}

func fetchSubmissionsOnce(userID string, fromSecond int64) ([]AtCoderSubmission, error) {
	req, err := submissionsRequest(userID, fromSecond)
	if err != nil {
		return nil, err
	}
	log.Printf("request to %s", req.URL.String())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code is not OK: %s", resp.Status)
	}
	r, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, err
	}
	var ss []AtCoderSubmission
	if err := json.NewDecoder(r).Decode(&ss); err != nil {
		return nil, err
	}
	return ss, nil
}

func fetchSubmissions(userID string) ([]AtCoderSubmission, error) {
	result := make([]AtCoderSubmission, 0)
	fromSecond := int64(0)
	for {
		ss, err := fetchSubmissionsOnce(userID, fromSecond)
		if err != nil {
			return nil, err
		}
		result = append(result, ss...)
		if len(ss) < submissionsPerPage {
			return result, nil
		}

		fromSecond = ss[len(ss)-1].EpochSecond
	}
}

// filter not AC submissions
func filterNotAC(ss []AtCoderSubmission) []AtCoderSubmission {
	result := make([]AtCoderSubmission, 0, len(ss))
	for _, s := range ss {
		if s.Result == "AC" {
			result = append(result, s)
		}
	}
	return result
}

func directoryPath(repoPath string, s AtCoderSubmission) string {
	return filepath.Join(repoPath, s.ContestID, s.ProblemID, strconv.Itoa(s.ID))
}

func filterDirsExist(repoPath string, ss []AtCoderSubmission) []AtCoderSubmission {
	result := make([]AtCoderSubmission, 0, len(ss))
	for _, s := range ss {
		if !dirExists(directoryPath(repoPath, s)) {
			result = append(result, s)
		}
	}
	return result
}

func archiveCmd() error {
	config, err := loadConfig()
	if err != nil {
		return err
	}
	ss, err := fetchSubmissions(config.Atcoder.UserID)
	if err != nil {
		return err
	}

	ss = filterNotAC(ss)
	ss = filterDirsExist(config.Atcoder.RepositoryPath, ss)
	sort.Slice(ss, func(i, j int) bool {
		return ss[i].EpochSecond < ss[j].EpochSecond
	})

	startTime := time.Now()
	log.Printf("Archiving %d code...", len(ss))

	for _, s := range ss {
		time.Sleep(time.Until(startTime.Add(1500 * time.Millisecond)))
		u := fmt.Sprintf("https://atcoder.jp/contests/%s/submissions/%d", s.ContestID, s.ID)
		log.Printf("Requesting... %s", u)

		resp, err := http.Get(u)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		startTime = time.Now()
		if err != nil {
			return err
		}

		doc, err := goquery.NewDocumentFromReader(resp.Body)
		if err != nil {
			return err
		}
		selection := doc.Find(".linenums")
		for i := 0; i < selection.Length(); i++ {
			code := selection.Eq(i).Text()
			if code == "" {
				return errors.New("Empty string...")
			}
			fileName := languageToFileName(s.Language)
			archiveDirPath := directoryPath(config.Atcoder.RepositoryPath, s)

			if err := archiveFile(code, fileName, archiveDirPath, s); err != nil {
				return fmt.Errorf("fail to archive the code at %s: %w", filepath.Join(archiveDirPath, fileName), err)
			}
			log.Printf("archived the code at %s", filepath.Join(archiveDirPath, fileName))
			// If the archive repo is the git repo
			// git add and git commit
			if !dirExists(filepath.Join(config.Atcoder.RepositoryPath, ".git")) {
				continue
			}

			r, err := git.PlainOpen(config.Atcoder.RepositoryPath)
			if err != nil {
				return err
			}

			w, err := r.Worktree()
			if err != nil {
				return err
			}
			// add source code
			dirRelativePath, err := filepath.Rel(config.Atcoder.RepositoryPath, archiveDirPath)
			if err != nil {
				return err
			}
			_, err = w.Add(filepath.Join(dirRelativePath, fileName))
			if err != nil {
				return err
			}

			message := fmt.Sprintf("✅ %s %s %dms %s", s.ContestID, s.ProblemID, s.ExecutionTime, u)
			_, err = w.Commit(message, &git.CommitOptions{
				Author: &object.Signature{
					Name:  s.UserID,
					Email: config.Atcoder.UserEmail,
					When:  time.Unix(s.EpochSecond, 0),
				},
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func editCmd() error {
	home, err := homedir.Dir()
	if err != nil {
		return err
	}
	configFile := filepath.Join(home, "."+appName, "config.json")
	// Config file not found, force to run an init cmd
	if !fileExists(configFile) {
		return initCmd(true)
	}

	editor := os.Getenv("EDITOR")
	if editor != "" {
		c := exec.Command(editor, configFile)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}

	return open.Run(configFile)
}

func main() {
	app := cli.App{
		Name: "procon-gardener", Usage: "archive your AC submissions",
		Commands: []*cli.Command{
			{
				Name:    "archive",
				Aliases: []string{"a"},
				Usage:   "archive your AC submissions",
				Action: func(c *cli.Context) error {
					return archiveCmd()
				},
			},
			{
				Name:    "init",
				Aliases: []string{"i"},
				Usage:   "initialize your config",
				Action: func(c *cli.Context) error {
					return initCmd(true)
				},
			},
			{
				Name:    "edit",
				Aliases: []string{"e"},
				Usage:   "edit your config file",
				Action: func(c *cli.Context) error {
					return editCmd()
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}
