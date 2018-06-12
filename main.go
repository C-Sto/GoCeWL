package main

import (
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"./libgocewl"
	"golang.org/x/net/proxy"
	//"github.com/c-sto/GoCeWL/libgocewl"
	"github.com/PuerkitoBio/goquery"
)

var version = "0.0.1"
var client *http.Client
var cfg libgocewl.Config

type SpiderPage struct {
	Url   string
	Depth int
}

func main() {

	cfg = libgocewl.Config{}
	flag.IntVar(&cfg.Threads, "t", 1, "Number of concurrent threads")
	flag.StringVar(&cfg.Url, "u", "", "Url to spider")
	flag.StringVar(&cfg.Localpath, "o", "."+string(os.PathSeparator)+"cewl.txt", "Local file to dump into")
	flag.BoolVar(&cfg.SSLIgnore, "k", false, "Ignore SSL check")
	flag.StringVar(&cfg.ProxyAddr, "p", "", "Proxy configuration options in the form ip:port eg: 127.0.0.1:9050")
	flag.IntVar(&cfg.Depth, "d", 3, "Depth of spider")
	flag.Parse()

	h, err := url.Parse(cfg.Url)
	if err != nil {
		panic("url parse fail")
	}
	cfg.Host = h.Host
	httpTransport := &http.Transport{}
	client = &http.Client{Transport: httpTransport}

	//skip ssl errors if requested to
	httpTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: cfg.SSLIgnore}
	//http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: SSLIgnore}

	//user a proxy if requested to
	if cfg.ProxyAddr != "" {
		fmt.Println("Proxy set to: ", cfg.ProxyAddr)
		dialer, err := proxy.SOCKS5("tcp", cfg.ProxyAddr, nil, proxy.Direct)
		if err != nil {
			os.Exit(1)
		}
		httpTransport.Dial = dialer.Dial
	}

	printBanner(cfg)
	pages := make(chan SpiderPage, 1000)
	newPages := make(chan SpiderPage, 10000)
	wordChan := make(chan string, 1000)
	firstPage := SpiderPage{}
	firstPage.Url = cfg.Url
	firstPage.Depth = 0
	wg := &sync.WaitGroup{}

	wg.Add(1)
	pages <- firstPage
	fmt.Println("Starting spider..")
	go spiderWalker(wg, pages, newPages, wordChan)
	go spiderBrain(wg, pages, newPages)
	go spiderSilk(wg, wordChan)

	wg.Wait()

}

var totalWords = uint64(0)

func spiderSilk(wg *sync.WaitGroup, wordChan chan string) {
	words := make(map[string]bool)
	file, err := os.OpenFile(cfg.Localpath, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		panic("Can't open file for reading, is something wrong?")
	}
	defer file.Close()

	for {
		word := <-wordChan
		if _, ok := words[word]; !ok {
			words[word] = true
			file.WriteString(word + "\n")
			file.Sync()
			totalWords++
		}
		wg.Done()
	}
}

func spiderBrain(wg *sync.WaitGroup, pages chan SpiderPage, newpages chan SpiderPage) {
	checked := make(map[string]bool)
	for {
		candidate := <-newpages

		u, err := url.Parse(candidate.Url)
		if err != nil {
			wg.Done()
			continue
		}
		//ensure candidate fits requirements

		if _, ok := checked[candidate.Url]; !ok && //must have not checked it before
			candidate.Depth < cfg.Depth &&
			u.Host == cfg.Host { //must be within the depth range

			checked[candidate.Url] = true
			wg.Add(1)
			pages <- candidate
		}
		//must be within same domain
		//??
		wg.Done()
	}
}

func spiderWalker(wg *sync.WaitGroup, pages chan SpiderPage, newPages chan SpiderPage, wordChan chan string) {
	//read from the page queue, set a spider leg off to do it's work
	workers := make(chan struct{}, cfg.Threads)
	for {
		page := <-pages
		//push a worker into the channel (or wait until there is room)
		workers <- struct{}{}
		wg.Add(1)
		go spiderLeg(page, wg, workers, pages, newPages, wordChan)
		wg.Done()
	}
}

func spiderLeg(page SpiderPage, wg *sync.WaitGroup, workers chan struct{}, pages chan SpiderPage, newPages chan SpiderPage, wordChan chan string) {
	fmt.Println("Getting ", page.Url, "Depth ", page.Depth, "url queue ", len(pages), "total words ", totalWords)
	urls, words := workPage(page.Url) //get the page and split into urls and words
	if urls == nil && words == nil {
		<-workers //signal to the spider contrller that it's done
		wg.Done()
		return
	}
	twg := &sync.WaitGroup{}
	if page.Depth < cfg.Depth {
		twg.Add(1)
		go func() {
			for _, x := range urls {
				newPage := SpiderPage{}
				newPage.Url = x
				newPage.Depth = page.Depth + 1
				wg.Add(1)
				newPages <- newPage
			}
			twg.Done()
		}()
	}

	twg.Add(1)
	go func() {
		for _, x := range words {
			//add word to word set
			wg.Add(1)
			wordChan <- x
		}
		twg.Done()
	}()
	twg.Wait()

	<-workers //signal to the spider contrller that it's done
	wg.Done()
}

func workPage(page string) ([]string, []string) {
	//get page address
	content, err := libgocewl.GetThing(page, client)
	if err != nil {
		fmt.Println("Page Working Error: ", err)
		return nil, nil
	}
	//get urls
	urls := getUrls(content)
	//get words
	words := getWords(content)

	return urls, words
}

//returns a slice of strings containing urls
func getUrls(page []byte) []string {

	ret := []string{}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return nil
	}

	doc.Find("*").Each(func(index int, item *goquery.Selection) {
		linkTag := item
		link, _ := linkTag.Attr("href")
		if len(link) > 0 {
			//don't bother with images, js or css
			bad := false
			for _, x := range []string{"js", "css", "png", "jpg", "gif", "pdf"} {
				if strings.HasSuffix(link, x) {
					bad = true
				}
			}
			if !bad {
				ret = append(ret, link)
			}
		}
	})

	return ret
}

//returns a slice of strings containing individual words
func getWords(page []byte) []string {
	ret := []string{}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return nil
	}
	title := doc.Find("title").Contents().Text() //grab the title
	ret = append(ret, strings.Fields(title)...)
	var meta string
	doc.Find("meta").Each(func(index int, item *goquery.Selection) {
		if item.AttrOr("name", "") == "description" {
			meta = item.AttrOr("content", "")
		}
	})
	ret = append(ret, strings.Fields(meta)...)

	var body string

	//ignore script elements
	doc.Find("script").Each(func(i int, el *goquery.Selection) {
		el.Remove()
	})

	//also ignore style
	doc.Find("style").Each(func(i int, el *goquery.Selection) {
		el.Remove()
	})

	//get body text. I think this will work?
	body = doc.Text()

	ret = append(ret, strings.Fields(body)...)

	return ret
}

func printBanner(cfg libgocewl.Config) {
	//todo: include settings in banner
	fmt.Println(strings.Repeat("=", 20))
	fmt.Println("GoCeWL V" + version)
	fmt.Println("Poorly hacked together by C_Sto")
	fmt.Println(strings.Repeat("=", 20))
}
