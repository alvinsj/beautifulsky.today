package twitter

import (
	"fmt"
	"github.com/garyburd/redigo/redis"
	"github.com/gin-gonic/gin"
	"github.com/kurrik/oauth1a"
	"github.com/kurrik/twittergo"
	"net/http"
	"net/url"
	"os"
	"time"
)

type Twitter struct{}

var (
	TWITTER_COSUMER_KEY     = os.Getenv("TWITTER_CONSUMER_KEY")
	TWITTER_CONSUMER_SECRET = os.Getenv("TWITTER_CONSUMER_SECRET")
	REDISTOGO, _ = url.Parse(os.Getenv("REDISTOGO_URL"))
)


func (tw Twitter) LoadCredentials() (client *twittergo.Client, err error) {
	config := &oauth1a.ClientConfig{
		ConsumerKey:    TWITTER_COSUMER_KEY,
		ConsumerSecret: TWITTER_CONSUMER_SECRET,
	}
	user := oauth1a.NewAuthorizedConfig("", "")
	client = twittergo.NewClient(config, user)
	return
}


func (tw Twitter) Memoize(resp map[string]string, tweetId uint64, key string, value string) map[string]string {
	conn, _ := redis.Dial("tcp", REDISTOGO.Host)
	cache, _ := redis.String(conn.Do("HGET", fmt.Sprintf("tweet:%v", tweetId), key))
    if cache == "" {
		conn.Do("HSET", fmt.Sprintf("tweet:%v", tweetId), key, value)
		cache = value
	}
	resp[key] = cache
	return resp
}


func (tw Twitter) TweetsFromResults(c *gin.Context, results *twittergo.SearchResults, t chan map[string]string, done chan bool) {
	conn, _ := redis.Dial("tcp", REDISTOGO.Host)
	fmt.Printf("start TweetsFromResults \n")

	for _, tweet := range results.Statuses() {

		user := tweet.User()
		entities := tweet["entities"].(map[string]interface{})
		urls := entities["urls"].([]interface{})

		if len(urls) > 0 {
			url := urls[0].(map[string]interface{})

			resp := make(map[string]string)

			reply, _ := redis.Values(conn.Do("KEYS", fmt.Sprintf("tweet:%v", tweet.Id())))
			fmt.Printf("values: %v", reply)
			if len(reply) == 0 {
				conn.Do("RPUSH", "tweets", tweet.Id())
			}

			resp = tw.Memoize(resp, tweet.Id(), "tweet", fmt.Sprintf("%v", tweet.Text()))
			resp = tw.Memoize(resp, tweet.Id(), "image", fmt.Sprintf("%v", url["expanded_url"]))
			resp = tw.Memoize(resp, tweet.Id(), "user", fmt.Sprintf("%v (@%v) ", user.Name(), user.ScreenName()))
			resp = tw.Memoize(resp, tweet.Id(), "created", fmt.Sprintf("%v", tweet.CreatedAt().Format(time.RFC1123)))
			t <- resp
		}
	}
	fmt.Printf("end TweetsFromResults \n")
}


func (tw Twitter) RetrieveMaxId() (string, bool){
	conn, _ := redis.Dial("tcp", REDISTOGO.Host)
	l, _ := redis.Int(conn.Do("LLEN", "tweets"))
	reply, _ := redis.String( conn.Do("LINDEX", "tweets", l-1) )
	if reply == "" {
		return reply, false
	} else {
		return reply, true
	}
}


func (tw Twitter) ConstructParams() url.Values {
	query := url.Values{}
	query.Set("q", "#beautifulsky")
	query.Set("result_type", "mixed")
	query.Set("count", "100")
	
	if maxId, present := tw.RetrieveMaxId(); present {
		fmt.Printf("maxId: %v", maxId)
		query.Set("max_id", maxId)
	}
	return query
}


func (tw Twitter) SearchTweets( k chan *twittergo.SearchResults, r chan *twittergo.APIResponse) {
	var (
		err     error
		results *twittergo.SearchResults
		req     *http.Request
		resp    *twittergo.APIResponse
		client *twittergo.Client
	)
	client, _ = tw.LoadCredentials()

	query := tw.ConstructParams()

	url := fmt.Sprintf("/1.1/search/tweets.json?%v", query.Encode())
	req, err = http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Printf("Could not parse request: %v\n", err)
	}

	resp, err = client.SendRequest(req)
	if err != nil {
		fmt.Printf("Could not send request: %v\n", err)
	}

	results = &twittergo.SearchResults{}
	err = resp.Parse(results)
	if err != nil {
		fmt.Printf("Problem parsing response: %v\n", err)
	}

	k <- results
	close(k)
	r <- resp
}


func (tw Twitter) TweetsFromCache(t chan map[string]string, cacheDone chan bool) {
    conn, _ := redis.Dial("tcp", REDISTOGO.Host)

	l, _ := redis.Int(conn.Do("LLEN", "tweets"))
	for i:=0; i< l; i++ {
		tweetId, _ := redis.String(conn.Do("LINDEX", "tweets", i))
		var resp map[string]string = make(map[string]string)
		for _, key := range []string{"tweet","image","user","created"} {
			reply,_ := redis.String( conn.Do("HGET", "tweet:"+tweetId, key) )
			resp[key] = reply
		}
		t <- resp
	}
	cacheDone <- true
}


func (tw Twitter) PrintRateLimit(r chan *twittergo.APIResponse) {
	fmt.Printf("start PrintRateLimit \n")
	resp := <-r
	if resp.HasRateLimit() {
		fmt.Printf("Rate limit:           %v\n", resp.RateLimit())
		fmt.Printf("Rate limit remaining: %v\n", resp.RateLimitRemaining())
		fmt.Printf("Rate limit reset:     %v\n", resp.RateLimitReset())
	} else {
		fmt.Printf("Could not parse rate limit from response.\n")
	}
	fmt.Printf("end PrintRateLimit \n")
}