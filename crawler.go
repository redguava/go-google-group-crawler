package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func MkdirAll(group string) {
	fmt.Printf(":: mkdir %s/\n", group)
	os.MkdirAll(fmt.Sprintf("%s/threads", group), 0700)
	os.MkdirAll(fmt.Sprintf("%s/msgs", group), 0700)
	os.MkdirAll(fmt.Sprintf("%s/mbox", group), 0700)
	os.MkdirAll(fmt.Sprintf("%s/mbox/cur", group), 0700)
	os.MkdirAll(fmt.Sprintf("%s/mbox/new", group), 0700)
	os.MkdirAll(fmt.Sprintf("%s/mbox/tmp", group), 0700)
}

func DumpLinksFromUrl(t string, group string, url string, cookies []http.Cookie, output_filename string) (int, int) {
	r_total, _ := regexp.Compile("<i>[^<]*?([0-9]+) *- *([0-9]+) of ([0-9]+)[^<]*?</i>")
	r_url, _ := regexp.Compile("\"(https?://.*?)\"")
	r_d, _ := regexp.Compile(fmt.Sprintf("/d/%s/%s", t, group))

	resp, err := AuthRequest(url, cookies)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	output_file, err := os.Create(output_filename)
	if err != nil {
		log.Fatal(err)
	}

	total := 0
	count := 0

	for scanner.Scan() {
		text := scanner.Text()

		if r_total.MatchString(text) {
			match := r_total.FindStringSubmatch(text)
			//log.Output(0, strings.Join(match, " - "))
			a, _ := strconv.Atoi(match[1])
			b, _ := strconv.Atoi(match[2])
			c, _ := strconv.Atoi(match[3])
			t := []int{a, b, c}
			sort.Ints(t)
			total = t[2]
		} else if r_url.MatchString(text) {
			match_url := r_url.FindAllStringSubmatch(text, -1)
			for _, m_url := range match_url {
				if r_d.MatchString(m_url[1]) {
					output_file.WriteString(m_url[1])
					output_file.WriteString("\n")
					count++
				}
			}
		}
	}

	return total, count
}

func DownloadPageWorker(id int, t string, group string, url string, cookies []http.Cookie, output_prefix string, jobs <-chan [2]int, results chan<- int) {
	for r := range jobs {
		fmt.Printf(":: Download %s from %s[%d-%d] by worker[%d]\n", t, group, r[0], r[1], id)
		url_with_range := fmt.Sprintf("%s[%d-%d]", url, r[0], r[1])
		output_filename := fmt.Sprintf("%s.%d.%d", output_prefix, r[0], r[1])
		DumpLinksFromUrl(t, group, url_with_range, cookies, output_filename)
	}
	results <- id
}

func DownloadPages(t string, group string, url string, cookies []http.Cookie, output_prefix string, workers int) {
	total, count := DumpLinksFromUrl(t, group, url, cookies, output_prefix+".0")

	if total == count {
		return
	}

	jobs := make(chan [2]int, total/100+1)
	jobs <- [2]int{count + 1, 100}
	for i := 1; i < total/100; i++ {
		jobs <- [2]int{i*100 + 1, (i + 1) * 100}
	}
	if total > 100 && total%100 > 0 {
		jobs <- [2]int{total - total%100, total}
	}
	close(jobs)

	results := make(chan int)
	for i := 0; i < workers; i++ {
		go DownloadPageWorker(i, t, group, url, cookies, output_prefix, jobs, results)
	}
	for i := 0; i < workers; i++ {
		<-results
	}
}
func AuthRequest(url string, cookies []http.Cookie) (resp *http.Response, err error) {
	log.Output(0, url)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	for _, item := range cookies {
		req.AddCookie(&item)
	}
	//fmt.Println(req.Cookies())

	client := &http.Client{}
	resp, err = client.Do(req)
	if err != nil {
		return
	}
	//fmt.Println("HTTP Response Status:", resp.StatusCode, http.StatusText(resp.StatusCode))

	return resp, err
}

func DownloadThreads(org string, group string, workers int, cookies []http.Cookie) {
	url := fmt.Sprintf("https://groups.google.com/a/%s/forum/?_escaped_fragment_=forum/%s", org, group)
	output_prefix := fmt.Sprintf("%s/threads/t", group)
	fmt.Printf(":: Download topic from %s - %s\n", org, group)
	DownloadPages("topic", group, url, cookies, output_prefix, workers)
}

func DownloadMessagesWorker(id int, group string, workers int, jobs <-chan string, results chan<- int, cookies []http.Cookie) {
	output_prefix := fmt.Sprintf("%s/msgs/m", group)
	for url := range jobs {
		ss := strings.Split(url, "/")
		msg_id := ss[len(ss)-1]
		fmt.Printf(":: Download msg from %s/%s by worker[%d]\n", group, msg_id, id)
		DownloadPages("msg", group, url, cookies, fmt.Sprintf("%s.%s", output_prefix, msg_id), workers)
	}
	results <- 1
}

func DownloadMessages(group string, workers int, cookies []http.Cookie) {
	dir := fmt.Sprintf("%s/threads/", group)
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Fatal(err)
	}

	jobs := make(chan string, 100)

	results := make(chan int)
	for i := 0; i < workers; i++ {
		go DownloadMessagesWorker(i, group, workers, jobs, results, cookies)
	}

	for _, f := range files {
		file, err := os.Open(fmt.Sprintf("%s/%s", dir, f.Name()))
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			text := scanner.Text()
			url := strings.Replace(text, "/d/topic/", "/forum/?_escaped_fragment_=topic/", -1)
			jobs <- url
		}
	}

	close(jobs)
	for i := 0; i < workers; i++ {
		<-results
	}
}

func DownloadRawMessage(url string, cookies []http.Cookie, output_filename string) {
	resp, err := AuthRequest(url, cookies)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	output_file, err := os.Create(output_filename)
	if err != nil {
		log.Fatal(err)
	}

	_, err = io.Copy(output_file, resp.Body)
	if err != nil {
		log.Fatal(err)
	}
}

func DownloadRawMessagesWorker(id int, group string, workers int, jobs <-chan string, results chan<- int, cookies []http.Cookie) {
	output_prefix := fmt.Sprintf("%s/mbox/cur/m", group)
	for url := range jobs {
		ss := strings.Split(url, "/")
		msg_id := fmt.Sprintf("%s.%s", ss[len(ss)-2], ss[len(ss)-1])
		fmt.Printf(":: Download raw msg from %s/%s by worker[%d]\n", group, msg_id, id)
		output_filename := output_prefix + "." + msg_id
		DownloadRawMessage(url, cookies, output_filename)
	}
	results <- 1
}

func DownloadRawMessages(group string, workers int, cookies []http.Cookie) {
	dir := fmt.Sprintf("%s/msgs/", group)
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Fatal(err)
	}

	jobs := make(chan string, 100)

	results := make(chan int)
	for i := 0; i < workers; i++ {
		go DownloadRawMessagesWorker(i, group, workers, jobs, results, cookies)
	}

	for _, f := range files {
		file, err := os.Open(fmt.Sprintf("%s/%s", dir, f.Name()))
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			text := scanner.Text()
			url := strings.Replace(text, "/d/msg/", "/forum/message/raw?msg=", -1)
			jobs <- url
		}
	}

	close(jobs)
	for i := 0; i < workers; i++ {
		<-results
	}
}

func ReadCookies() []http.Cookie {
	dat, err := ioutil.ReadFile("./cookies.txt")
	if err != nil {
		log.Fatal(err)
	}

	cookie_split := regexp.MustCompile(`; *`)
	value_split := regexp.MustCompile(`=`)

	cookie_list := cookie_split.Split(string(dat), -1)
	result := make([]http.Cookie, len(cookie_list))
	for i, item := range cookie_list {
		cookie_values := value_split.Split(item, 2)
		result[i] = http.Cookie{Name: strings.TrimSpace(cookie_values[0]), Value: strings.TrimSpace(cookie_values[1])}
	}

	return result
}

func main() {
	orgPtr := flag.String("o", "redguava.com.au", "Organization Name")
	groupPtr := flag.String("g", "", "Group name")
	workerNumPtr := flag.Int("t", 1, "Threads count")
	cookies := ReadCookies()

	flag.Parse()

	if *groupPtr == "" {
		flag.Usage()
		return
	}
	MkdirAll(*groupPtr)
	DownloadThreads(*orgPtr, *groupPtr, *workerNumPtr, cookies)
	DownloadMessages(*groupPtr, *workerNumPtr, cookies)
	DownloadRawMessages(*groupPtr, *workerNumPtr, cookies)
}
