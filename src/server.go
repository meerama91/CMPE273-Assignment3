package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/julienschmidt/httprouter"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
)

type Resource2 struct {
	Id                           bson.ObjectId `bson:"_id" json:"id"`
	Status                       string        `bson:"status" json:"status"`
	Starting_from_location_id    string        `bson:"starting_from_location_id" json:"starting_from_location_id"`
	Location_ids                 []string      `bson:"location_ids" json:"location_ids"`
	Next_destination_location_id string        `bson:"next_destination_location_id" json:"next_destination_location_id"`
	Best_route_location_ids      []string      `bson:"best_route_location_ids" json:"best_route_location_ids"`
	Total_uber_costs             int           `bson:"total_uber_costs" json:"total_uber_costs"`
	Total_uber_duration          int           `bson:"total_uber_duration" json:"total_uber_duration"`
	Total_distance               float64       `bson:"total_distance" json:"total_distance"`
	Uber_wait_time_eta           int           `bson:"uber_wait_time_eta" json:"uber_wait_time_eta"`
	Pointer                      int           `bson:"pointer"`
}

type Resource struct {
	Id         bson.ObjectId `bson:"_id" json:"id"`
	Name       string        `bson:"name" json:"name"`
	Address    string        `bson:"address" json:"address"`
	City       string        `bson:"city" json:"city"`
	State      string        `bson:"state" json:"state"`
	Zip        string        `bson:"zip" json:"zip"`
	Coordinate Coord         `bson:"coord" json:"coord"`
}

type Coord struct {
	Lat  float64
	Long float64
}

type UserController struct {
	session *mgo.Session
}

type Graph struct {
	Cost     int
	Duration int
	Distance float64
}

type Product struct {
	Products []struct {
		ProductId string `json:"product_id"`
	}
}

type PriceEstimate struct {
	Prices []struct {
		ProductId       string  `json:"product_id"`
		CurrencyCode    string  `json:"currency_code"`
		DisplayName     string  `json:"display_name"`
		Estimate        string  `json:"estimate"`
		LowEstimate     int     `json:"low_estimate"`
		HighEstimate    int     `json:"high_estimate"`
		SurgeMultiplier float64 `json:"surge_multiplier"`
		Duration        int     `json:"duration"`
		Distance        float64 `json:"distance"`
	}
}

type UberResponse struct {
	Eta int `json:"eta"`
}

var uc UserController
var uri, ServerToken string
var routeIndex []int
var Adj [][]Graph
var trackRoute []string

func getSession() *mgo.Session {
	// Connect to our local mongo
	s, err := mgo.Dial(uri)

	// Check if connection error, is mongo running?
	if err != nil {
		panic(err)
	}
	return s
}

func NewUserController(s *mgo.Session) UserController {
	return UserController{s}
}

func getLL(id string) Coord {

	response := Resource{}
	err := uc.session.DB("locations").C("locations").FindId(bson.ObjectIdHex(id)).One(&response)
	if err != nil {
		//rw.WriteHeader(404)
		log.Println("error retrieving")
	}
	result := response.Coordinate
	return result

}

func getPrice(url string) Graph {
	log.Println("url is",url)
	res, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	body, err := ioutil.ReadAll(res.Body)
	res.Body.Close()
	res2 := &PriceEstimate{}
	json.Unmarshal(body, res2)

	resp := Graph{}
	resp.Cost = res2.Prices[0].LowEstimate
	resp.Duration = res2.Prices[0].Duration
	resp.Distance = res2.Prices[0].Distance

	return resp
}

func getter(rw http.ResponseWriter, req *http.Request, p httprouter.Params) {
	log.Println("going into get")
	id := p.ByName("trip_id")
	if !bson.IsObjectIdHex(id) {
		rw.WriteHeader(404)
		return
	}
	fmt.Println("id id ", id)
	oid := bson.ObjectIdHex(id)
	fmt.Println("oid id ", oid)
	response := Resource2{}
	err := uc.session.DB("locations").C("trips").FindId(oid).One(&response)

	if err != nil {
		rw.WriteHeader(404)
		return
	}
	responseJson, _ := json.Marshal(response)
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(200)
	fmt.Fprintf(rw, "%s", responseJson)
}

func getTotals(arr []int) Graph {

	res := Graph{}
	res = Adj[0][arr[0]]
	for i := 1; i < len(arr); i++ {
		res.Cost = res.Cost + Adj[arr[i-1]][arr[i]].Cost
		res.Distance = res.Distance + Adj[arr[i-1]][arr[i]].Distance
		res.Duration = res.Duration + Adj[arr[i-1]][arr[i]].Duration
	}
	res.Cost = res.Cost + Adj[arr[len(arr)-1]][0].Cost
	res.Duration = res.Duration + Adj[arr[len(arr)-1]][0].Duration
	res.Distance = res.Distance + Adj[arr[len(arr)-1]][0].Distance
	return res
}

func getProductId(src Coord) string {
	params := map[string]string{
		"latitude":  strconv.FormatFloat(src.Lat, 'f', 2, 32),
		"longitude": strconv.FormatFloat(src.Long, 'f', 2, 32),
	}
	urlParams := "?"
	params["server_token"] = ServerToken
	for k, v := range params {
		if len(urlParams) > 1 {
			urlParams += "&"
		}
		urlParams += fmt.Sprintf("%s=%s", k, v)
	}

	url := fmt.Sprintf("https://api.uber.com/v1/%s%s", "products", urlParams)
	res, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	body, err := ioutil.ReadAll(res.Body)
	log.Println("the body of product response", body)
	res.Body.Close()
	res2 := &Product{}
	json.Unmarshal(body, res2)
	log.Println("respo for product is", res2.Products[0].ProductId)
	return res2.Products[0].ProductId

}

func creator(rw http.ResponseWriter, req *http.Request, p httprouter.Params) {

	request := Resource2{}
	response := Resource2{}
	json.NewDecoder(req.Body).Decode(&request)
	dest := request.Location_ids
	l := len(dest) + 1
	destLL := make([]Coord, l)

	Adj = make([][]Graph, l)
	APIUrl := "https://api.uber.com/v1/%s%s"
	endpoint := "estimates/price"

	for i := 1; i < l; i++ {
		destLL[i] = getLL(dest[i-1])
	}
	destLL[0] = getLL(request.Starting_from_location_id)

	for i := 0; i < l; i++ {
		row := make([]Graph, l)
		for j := 0; j < l; j++ {
			if i != j {
				Params := map[string]string{
					"start_latitude":  strconv.FormatFloat(destLL[i].Lat, 'f', 6, 32),
					"start_longitude": strconv.FormatFloat(destLL[i].Long, 'f', 6, 32),
					"end_latitude":    strconv.FormatFloat(destLL[j].Lat, 'f', 6, 32),
					"end_longitude":   strconv.FormatFloat(destLL[j].Long, 'f', 6, 32),
				}

				urlParams := "?"
				Params["server_token"] = ServerToken
				for k, v := range Params {
					if len(urlParams) > 1 {
						urlParams += "&"
					}
					urlParams += fmt.Sprintf("%s=%s", k, v)
				}
				url := fmt.Sprintf(APIUrl, endpoint, urlParams)
				row[j] = getPrice(url)
			} else {
				row[j] = Graph{}
			}
		}
		Adj[i] = row
	}

	emptybest := Graph{}
	routeIndex = make([]int, len(dest))
	best := Graph{}
	temp := Graph{}
	count := 1
	var helper func([]int, int)
	res := [][]int{}
	helper = func(arr []int, n int) {
		if n == 1 {
			tmp := make([]int, len(arr))
			copy(tmp, arr)
			fmt.Println("element is: ", tmp)
			fmt.Println("count is: ", count)
			count++

			temp = getTotals(tmp)
			if best == emptybest {
				best = temp
				routeIndex = tmp
			} else {
				if best.Cost > temp.Cost {
					best = temp
					routeIndex = tmp
				}
			}

			log.Println("the perms are", tmp)
			res = append(res, tmp)
		} else {
			for i := 0; i < n; i++ {
				helper(arr, n-1)
				if n%2 == 1 {
					tmp := arr[i]
					arr[i] = arr[n-1]
					arr[n-1] = tmp
				} else {
					tmp := arr[0]
					arr[0] = arr[n-1]
					arr[n-1] = tmp
				}
			}
		}
	}
	temparr := make([]int, len(dest))
	for m := 1; m <= len(dest); m++ {
		temparr[m-1] = m
	}
	helper(temparr, len(dest))
	route := make([]string, len(dest))
	for n := 0; n < len(dest); n++ {
		route[n] = dest[routeIndex[n]-1]
	}
	response.Status = "planning"
	response.Id = bson.NewObjectId()
	response.Starting_from_location_id = request.Starting_from_location_id
	response.Best_route_location_ids = route
	response.Total_uber_costs = best.Cost
	response.Total_uber_duration = best.Duration
	response.Total_distance = best.Distance
	response.Location_ids = request.Location_ids

	response.Pointer = -1
	err := uc.session.DB("locations").C("trips").Insert(response)
	if err != nil {
		rw.WriteHeader(404)
		log.Println("error", err)
		return
	}
	responseJson, _ := json.Marshal(response)
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(201)
	fmt.Fprintf(rw, "%s", responseJson)
}

func getEta(src string, dest string) int {
	stll := Coord{}
	dell := Coord{}
	res2 := &UberResponse{}
	stll = getLL(src)
	dell = getLL(dest)
	stlat := strconv.FormatFloat(stll.Lat, 'f', 6, 32)
	stlong := strconv.FormatFloat(stll.Long, 'f', 6, 32)
	delat := strconv.FormatFloat(dell.Lat, 'f', 6, 32)
	delong := strconv.FormatFloat(dell.Long, 'f', 6, 32)
	productId := getProductId(stll)
	url := "https://sandbox-api.uber.com/v1/requests"
	var jsonprep string = `{"start_latitude":"` + stlat + `","start_longitude":"` + stlong + `","end_latitude":"` + delat + `","end_longitude":"` + delong + `","product_id":"` + productId + `","scope":"request"}`
	var jsonStr = []byte(jsonprep)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzY29wZXMiOlsicmVxdWVzdCJdLCJzdWIiOiJkNDU5NjA5My1hOTI4LTQ3ODItODgzYy05MzU0OTdjMjU1MDciLCJpc3MiOiJ1YmVyLXVzMSIsImp0aSI6ImZlOGY3MTU0LTNlOWUtNDdlNC04MDY3LTk4YzNhNmY1MmRkMSIsImV4cCI6MTQ1MDcyOTUwMSwiaWF0IjoxNDQ4MTM3NTAwLCJ1YWN0IjoiT1puRUFaT1dmWFhLeTh6c29rOFd1b3FqS211eGpoIiwibmJmIjoxNDQ4MTM3NDEwLCJhdWQiOiJLM3dtY09MTm5PMTI0aTVjVEF3M1NhN281U09WN29KUiJ9.YLtQUqjN8Hs86-4Ry-CfbO7gm6J_vrfQYTWMQfY4rVvZwo_kwME-7JyUHiO8I9leWc7YqUJHWNhrPB_okAAdsyPKVpKDcNlwiI5l5Y_awNIpnVTHENshWUPowXLtkmL1wZimJB80Uf1Lt2QO_FOanXCPp_pKX90ZSeFlwqr6p63Hvprna_4ceX3gyTnQagot10rrbOT7QrbDhTo7LF8d6g4TnUPPuPywcv4hIlwp7jt5rP0Up1I9iY56FIaZkdA-rWRb0UNAKoL0RGNDDPPnl4id3IhLG_xkKd8qkiHpRv73xuNI-oY1qjkF42bbSR4Upz-pmbfrmN8kCQWMIwdx9g")
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	body, _ := ioutil.ReadAll(resp.Body)
	json.Unmarshal(body, res2)
	log.Println("response from uber post Body:", string(body))
	log.Println("final eta is ", res2)
	return res2.Eta

}

func updater(rw http.ResponseWriter, req *http.Request, p httprouter.Params) {
	log.Println("entered put")
	id := p.ByName("trip_id")
	request := Resource2{}
	retr := Resource2{}
	var src, dest string
	log.Println("the id is ", id)
	json.NewDecoder(req.Body).Decode(&request)

	log.Println("converted id")
	err := uc.session.DB("locations").C("trips").FindId(bson.ObjectIdHex(id)).One(&retr)
	log.Println("got routes", retr)
	if err != nil {
		//rw.WriteHeader(404)
		log.Println("error retrieving", err)
	}
	l := len(retr.Best_route_location_ids)
	route := retr.Best_route_location_ids
	response := Resource2{}

	Query := bson.M{"_id": bson.ObjectIdHex(id)}
	if retr.Pointer == -1 {
		src = retr.Starting_from_location_id
		dest = route[0]
		eta := getEta(src, dest)
		change := bson.M{"$set": bson.M{"pointer": 0, "status": "requesting", "uber_wait_time_eta": eta, "next_destination_location_id": dest}}
		err = uc.session.DB("locations").C("trips").Update(Query, change)
	} else if retr.Pointer == -2 {
		change := bson.M{"$set": bson.M{"status": "finished", "uber_wait_time_eta": 0, "next_destination_location_id": ""}}
		err = uc.session.DB("locations").C("trips").Update(Query, change)
	} else {
		src = route[retr.Pointer]
		if retr.Pointer == l-1 {
			dest = retr.Starting_from_location_id
			eta := getEta(src, dest)
			change := bson.M{"$set": bson.M{"pointer": -2, "status": "requesting", "uber_wait_time_eta": eta, "next_destination_location_id": dest}}
			err = uc.session.DB("locations").C("trips").Update(Query, change)
		} else {
			dest = route[retr.Pointer+1]
			eta := getEta(src, dest)
			change := bson.M{"$set": bson.M{"pointer": retr.Pointer + 1, "status": "requesting", "uber_wait_time_eta": eta, "next_destination_location_id": dest}}
			err = uc.session.DB("locations").C("trips").Update(Query, change)
		}

	}
	log.Println("old data is ", retr)

	err = uc.session.DB("locations").C("trips").FindId(bson.ObjectIdHex(id)).One(&response)
	if err != nil {
		rw.WriteHeader(404)
		return
	}
	log.Println("new data is ", response)
	responseJson, _ := json.Marshal(response)
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(200)
	fmt.Fprintf(rw, "%s", responseJson)

}

func main() {

	mux := httprouter.New()
	uri = "mongodb://muser:mpassword@ds029804.mongolab.com:29804/locations"
	ServerToken = "STHBM1UKbdgVR5xlbWWx11amIHSolyxnl_Nm1MFx"
	uc = NewUserController(getSession())
	mux.POST("/trips", creator)
	mux.GET("/trips/:trip_id", getter)
	mux.PUT("/trips/:trip_id/request", updater)
	server := http.Server{
		Addr:    "0.0.0.0:1234",
		Handler: mux,
	}
	server.ListenAndServe()
}
