package main
// shite
import (
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"time"
)

var instances = []string{
	"https://baresearch.org/",
	"https://copp.gg/",
	"https://darmarit.org/searx/",
	"https://etsi.me/",
	"https://fairsuch.net/",
	"https://find.xenorio.xyz/",
	"https://kantan.cat/",
	"https://metacat.online/",
	"https://northboot.xyz/",
	"https://nyc1.sx.ggtyler.dev/",
	"https://ooglester.com/",
	"https://opnxng.com/",
	"https://paulgo.io/",
	"https://priv.au/",
	"https://s.mble.dk/",
	"https://search.080609.xyz/",
	"https://search.2b9t.xyz/",
	"https://search.ashisgreat.xyz/",
	"https://search.buddyverse.net/",
	"https://search.canine.tools/",
	"https://search.charliewhiskey.net/",
	"https://search.citw.lgbt/",
	"https://search.einfachzocken.eu/",
	"https://search.hbubli.cc/",
	"https://search.im-in.space/",
	"https://search.indst.eu/",
	"https://search.inetol.net/",
	"https://search.ipsys.bf/",
	"https://search.ipv6s.net/",
	"https://search.leptons.xyz/",
	"https://search.mdosch.de/",
	"https://search.nerdvpn.de/",
	"https://search.oh64.moe/",
	"https://search.ononoki.org/",
	"https://search.privacyredirect.com/",
	"https://search.projectsegfau.lt/",
	"https://search.rhscz.eu/",
	"https://search.rowie.at/",
	"https://search.sapti.me/",
	"https://search.system51.co.uk/",
	"https://search.url4irl.com/",
	"https://searx.dresden.network/",
	"https://searx.foobar.vip/",
	"https://searx.foss.family/",
	"https://searx.juancord.xyz/",
	"https://searx.lunar.icu/",
	"https://searx.mbuf.net/",
	"https://searx.mxchange.org/",
	"https://searx.namejeff.xyz/",
	"https://searx.oloke.xyz/",
	"https://searx.ox2.fr/",
	"https://searx.party/",
	"https://searx.perennialte.ch/",
	"https://searx.ppeb.me/",
	"https://searx.ro/",
	"https://searx.sev.monster/",
	"https://searx.stream/",
	"https://searx.tiekoetter.com/",
	"https://searx.tuxcloud.net/",
	"https://searx.zhenyapav.com/",
	"https://searxng.biz/",
	"https://searxng.deliberate.world/",
	"https://searxng.f24o.zip/",
	"https://searxng.hweeren.com/",
	"https://searxng.shreven.org/",
	"https://searxng.site/",
	"https://searxng.website/",
	"https://seek.fyi/",
	"https://sx.catgirl.cloud/",
	"https://www.gruble.de/",
}

func main() {
	rand.Seed(time.Now().UnixNano())

	http.HandleFunc("/search", searchHandler)

	fmt.Println("Starting server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing search query", http.StatusBadRequest)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	instance := instances[rand.Intn(len(instances))]

	searchURL := fmt.Sprintf("%s?q=%s&format=%s", instance, url.QueryEscape(query), format)

	resp, err := http.Get(searchURL)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch from instance: %s", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Failed to read response body", http.StatusInternalServerError)
		return
	}

	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "text/plain")
	}

	w.Write(body)
}
