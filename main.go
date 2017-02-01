package main

import (
	"fmt"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/mysql"
	"regexp"
	"strings"
	"os"
	"net/http"
	"io/ioutil"
	"time"
	"math/rand"
)

const (
	maxWorkers = 50
	maxUrlDepth = 0
)

//var urls = []string {"http://mito.hu"}

//var dbSettings = mysql.ConnectionURL{
//    Host:     "localhost:17008",
//    Database: "database",
//    User:     "dev",
//    Password: "devpass",
//}

// url records per domain
//SELECT count(id) as count,
//SUBSTRING_INDEX(REPLACE(REPLACE(address,'http://',''),'https://',''),'/',1) as domain
//FROM urls
//WHERE status = 1
//GROUP BY domain
//ORDER BY count ASC

type Url struct {
	gorm.Model
	Address string `gorm:"type:text"`
	Status int
	Depth int
	Rnd float64
}

type Relation struct {
	gorm.Model
	AddressID uint
	ParentID uint
}

func main() {
	db, err := gorm.Open("mysql", "dev:devpass@(localhost:17008)/database?charset=utf8&parseTime=True&loc=Local")
	if err != nil {
		panic("failed to connect database")
	}
	defer db.Close()

	// DB debug mode
	// db.LogMode(true)

	// resetDB(db)

	timeout := time.Duration(5 * time.Second)
	client := http.Client {
    		Timeout: timeout,
	}

	var sem = make(chan int, maxWorkers)
	var urlChan = make(chan Url, maxWorkers)

	for {
		// get an url
		var url Url
		err := db.Where(&Url{Status: 1}).Where("depth <= ?", maxUrlDepth).Order("rnd").First(&url).Error
		if err != nil {
			fmt.Printf("DB get url error: %v\n", err)
			os.Exit(6)
		}
		// set status
		db.Model(&url).Updates(Url{Status: 2})

		// Block until there's capacity to process a request.
		sem <- 1
		urlChan <- url
		// Don't wait for handle to finish.
		go process(sem, urlChan, db, client)
	}
}

func process(sem chan int, urlChan chan Url, db *gorm.DB, client http.Client) {
	url := <- urlChan
	// get html code
	resp, err := client.Get(url.Address)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		db.Model(&url).Updates(Url{Status: -1})
		<-sem      // Done; enable next request to run.
		return
	}
	// reads html as a slice of bytes
	html, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		db.Model(&url).Updates(Url{Status: -1})
		<-sem      // Done; enable next request to run.
		return
	}
	// print html
	//fmt.Printf("%s\n", html)
	// get urls-from html code
	newUrls := parseLinks(html, url.Address)
	// save url-s
	saveLinks(db, newUrls, url.ID)

	// Done; enable next request to run.
	<-sem
}

func resetDB(db *gorm.DB) {
	// Drop model tables
	db.DropTableIfExists(&Url{})
	db.DropTableIfExists(&Relation{})

	// Migrate the schemas
	db.AutoMigrate(&Url{})
	db.AutoMigrate(&Relation{})

	// Add unique index
	db.Model(&Url{}).AddUniqueIndex("uqidx_adress", "address")

	// Add seed address(es)
	db.Create(&Url{Address: "http://mito.hu", Status: 1})
	db.Create(&Url{Address: "https://vimeo.com", Status: 1})
	db.Create(&Url{Address: "http://mupa.hu", Status: 1})
	db.Create(&Url{Address: "http://www.pinterest.com", Status: 1})
	db.Create(&Url{Address: "http://instagram.com", Status: 1})
	db.Create(&Url{Address: "http://www.youtube.com", Status: 1})
	db.Create(&Url{Address: "https://twitter.com", Status: 1})
	db.Create(&Url{Address: "http://index.hu", Status: 1})
	db.Create(&Url{Address: "http://origo.hu", Status: 1})
	db.Create(&Url{Address: "http://mek.oszk.hu", Status: 1})

}

func saveLinks(db *gorm.DB, addresses []string, parentID uint) {
	for _, address := range addresses {
		// select if this address already exists in the db, insert if it's not
		var url Url
		db.Where(Url{Address: address}).Attrs(Url{Status: 1, Depth:urlDepth(address), Rnd:rand.Float64()}).FirstOrCreate(&url)

		// add relation in both cases
		err := db.Create(&Relation{AddressID: url.ID, ParentID: parentID}).Error
		if err != nil {
			fmt.Printf("DB Insert error: %v\n", err)
		}
	}
}

func parseLinks(html []byte, origin string) []string {
	var parsedUrls = []string{}
	fmt.Printf("Processing: %s\n", origin)
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

		// csak duplikátumok szűrése
		if uniqueUrl(fullUrl, parsedUrls, origin) {
			parsedUrls = append(parsedUrls, fullUrl)
		}
	}

	//for _, pu := range parsedUrls {
	//	fmt.Println(pu)
	//}
	return parsedUrls
}

func urlDepth(url string) int {
	// remove http(s):// and trailing / if any
	baseUrl := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://"), "/")
	// count remaining "/" characters
	//fmt.Printf("base: %s, depth: %v\n", baseUrl, strings.Count(baseUrl, "/"))
	return strings.Count(baseUrl, "/")
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
	tmp := s[8:]
	tmp = strings.Replace(tmp, "//", "/", -1)
	return s[:8] + tmp
}