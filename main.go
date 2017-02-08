package main

import (
	"log"
	"regexp"
	"strings"
	"os"
	"net/http"
	"io/ioutil"
	"time"
	"crypto/sha1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

const (
	maxWorkers = 200
	maxUrlDepth = 0
)

type Url struct {
	ID bson.ObjectId `bson:"_id,omitempty"`
	Address string
	Status int
	Depth int
	Hash string
}

type Relation struct {
	ID bson.ObjectId `bson:"_id,omitempty"`
	AddressID bson.ObjectId
	ParentID bson.ObjectId
}

func main() {
	dbsession, err := mgo.Dial("localhost:17008")
	if err != nil {
		panic(err)
	}
	defer dbsession.Close()

	c := dbsession.DB("database").C("url")

	resetDB(dbsession)
	//os.Exit(255)

	// set indices
	index := mgo.Index{
		Key: []string{"hash"},
		Unique: true,
	}
	err = c.EnsureIndex(index)
	if err != nil {
		panic(err)
	}
	index2 := mgo.Index{
		Key: []string{"status", "depth", "hash"},
		Unique: true,
	}
	err = c.EnsureIndex(index2)
	if err != nil {
		panic(err)
	}

	timeout := time.Duration(5 * time.Second)
	client := http.Client {
    		Timeout: timeout,
	}

	var sem = make(chan int, maxWorkers)
	var urlChan = make(chan Url, maxWorkers)

	for {
		// get a random url
		url := Url{}
		err = c.Find(bson.M{"status": 1, "depth": maxUrlDepth}).Sort("hash").One(&url)
		if err != nil {
			log.Printf("DB get url error: %v\n", err)
			log.Printf("Not enough urls for all threads, waiting for more...\n")
			time.Sleep(1 * time.Second)
			continue
		}

		// set status
		err = c.Update(bson.M{"hash": url.Hash}, bson.M{"$set": bson.M{"status": 2}})
		if err != nil {
			log.Printf("DB update status error: %v\n", err)
			os.Exit(7)
		}

		// Block until there's capacity to process a request.
		sem <- 1
		urlChan <- url
		// Don't wait for handle to finish.
		go process(sem, urlChan, dbsession, client)
	}
}

func process(sem chan int, urlChan chan Url, db *mgo.Session, client http.Client) {
	start := time.Now()

	// start new db session for mongo
	dbsession := db.Copy()
	defer dbsession.Close()

	// collections
	c := dbsession.DB("database").C("url")
	rc := dbsession.DB("database").C("relation")

	// get url from channel
	url := <- urlChan

	f_start := time.Now()
	// get html code
	resp, err := client.Get(url.Address)
	if err != nil {
		log.Printf("Error: %v\n", err)
		c.Update(bson.M{"_id": url.ID}, bson.M{"$set": bson.M{"status": -1}})
		<-sem      // Done; enable next request to run.
		return
	}

	// read html as a slice of bytes
	html, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		log.Printf("Error: %v\n", err)
		c.Update(bson.M{"_id": url.ID}, bson.M{"$set": bson.M{"status": -1}})
		<-sem      // Done; enable next request to run.
		return
	}

	// print html
	//log.Printf("%s\n", html)

	p_start := time.Now()
	// get urls-from html code
	newUrls := parseLinks(html, url.Address)

	s_start := time.Now()
	// save url-s
	saveLinks(c, rc, newUrls, url.ID)

	// print some stats
	log.Printf("Processed %s: found %d new urls in %s [i: %s, f: %s, p: %s, s: %s]\n", url.Address, len(newUrls), time.Since(start), f_start.Sub(start), p_start.Sub(f_start), s_start.Sub(p_start), time.Since(s_start))

	// Done; enable next request to run.
	<-sem
}

func resetDB(dbsession *mgo.Session) {
	var seedUrls = []string {
		"http://mito.hu",
		"https://vimeo.com",
		"http://mupa.hu",
		"http://startlap.hu",
		"http://www.pinterest.com",
		"http://instagram.com",
		"http://www.youtube.com",
		"https://twitter.com",
		"http://index.hu",
		"http://origo.hu",
		"http://mek.oszk.hu",
	}

	// drop db
	dbsession.DB("database").DropDatabase()

	// Add seed address(es)
	c := dbsession.DB("database").C("url")
	for _, address := range seedUrls {
		hash := sha1.Sum([]byte(address))
		err := c.Insert(&Url{Address: address, Hash:string(hash[:]), Status: 1})
		if err != nil {
			log.Printf("DB Init Insert error: %v\n", err)
		}
	}
}

func saveLinks(c *mgo.Collection, rc *mgo.Collection, addresses []string, parentID bson.ObjectId) {
	for _, address := range addresses {
		// calculate address hash
		hash := sha1.Sum([]byte(address))
		hash_str := string(hash[:])

		// add url to the collection
		url := Url{Address: address, Status: 1, Depth: urlDepth(address), Hash: hash_str}
		c.Insert(url)

		// add relation to the collection
		url = Url{}
		err := c.Find(bson.M{"hash": hash_str}).One(&url)
		if err != nil {
			log.Printf("DB relation url lookup error: %v\n", err)
		}
		err = rc.Insert(&Relation{AddressID: url.ID, ParentID: parentID})
		if err != nil {
			log.Printf("DB Relation Insert error: %v\n", err)
		}
	}
}

func parseLinks(html []byte, origin string) []string {
	var parsedUrls = []string{}
	rx, _ := regexp.Compile("<a href=\"([htps:]*/[[:alnum:]_.~!*'();:@&=+$,/?#%-+]{2,})\"")
	res := rx.FindAllStringSubmatch(string(html), -1)
	for _, r := range res {
		// # és / hivatkozások szűrése
		if len(r) <= 1 {
			continue
		}
		var fullUrl string

		// relatív hivatkozás kezelése
		if string(r[1][:1]) == "/" {
			fullUrl = strings.TrimSpace(origin) + strings.TrimSpace(r[1])
		} else {
			fullUrl = strings.TrimSpace(r[1])
		}
		// replace "//" strings to "/" except for the protocol
		fullUrl = removeDuplicateDash(fullUrl)

		// remove "#" if present and everything after that
		fullUrl = trimStringFromHashMark(fullUrl)

		// remove trailing dash
		fullUrl = removeTrailingDash(fullUrl)

		// csak duplikátumok szűrése
		if uniqueUrl(fullUrl, parsedUrls, origin) {
			parsedUrls = append(parsedUrls, fullUrl)
		}
	}

	//for _, pu := range parsedUrls {
	//	log.Println(pu)
	//}
	return parsedUrls
}

func urlDepth(url string) int {
	// remove http(s):// and trailing / if any
	baseUrl := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://"), "www.")
	// count remaining "/" and "." characters
	return strings.Count(baseUrl, "/") + (strings.Count(baseUrl, ".") - 1)
}

func uniqueUrl(url string, parsedUrls []string, origin string) bool {
	urlLen := len(url)
	originLen := len(origin)
	if (originLen == urlLen) && strings.Compare(url, origin) == 0 {
		return false
	}
	for _, pu := range parsedUrls {
		if (len(pu) == urlLen) && (strings.Compare(url, pu) == 0) {
			return false
		}
	}
	return true
}

func trimStringFromHashMark(s string) string {
	if idx := strings.Index(s, "#"); idx != -1 {
		return s[:idx]
	}
	return s
}

func removeDuplicateDash(s string) string {
	if len(s) < 9 {
		return strings.Replace(s, "//", "/", -1)
	}
	tmp := s[8:]
	tmp = strings.Replace(tmp, "//", "/", -1)
	return s[:8] + tmp
}

func removeTrailingDash(s string) string {
	return strings.TrimSuffix(s, "/")
}
