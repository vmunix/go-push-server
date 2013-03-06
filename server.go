package main

import (
        "code.google.com/p/go.net/websocket"
        "net"
        "net/http"
        "log"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"./uuid"
)

const (
	HOST_NAME = "localhost"
	PORT_NUMBER = "8080"
	APPSERVER_API_PREFIX = "/notify/"
)

type Client struct {
	Websocket *websocket.Conn
	UAID string
	Ip   string
	Port float64
}

type Channel struct {
	uaid string

	ChannelID string `json:"channelID"`
	Version string   `json:"version"`
}


// Mapping from a UAID to the Client object
var gConnectedClients map[string]*Client;

// Mapping from a UAID to all channels owned by that UAID
var gUAIDToChannel map[string][]*Channel;

// Mapping from a ChannelID to the cooresponding Channel
var gChannelIDToChannel map[string] *Channel;

func handleRegister(client *Client, f map[string]interface{}) {
	log.Println(" -> handleRegister");

	if (f["channelID"] == nil) {
		log.Println("channelID is missing!");
		return;
	}

	var channelID = f["channelID"].(string);

	// TODO https!
	var pushEndpoint = "http://" + HOST_NAME + ":" + PORT_NUMBER + APPSERVER_API_PREFIX + channelID;

	channel := &Channel{client.UAID, channelID, ""};

	gUAIDToChannel[client.UAID] = append(gUAIDToChannel[client.UAID], channel);
	gChannelIDToChannel[channelID] = channel;

	type RegisterResponse struct {
		Name string          `json:"messageType"`
		Status int           `json:"status"`
		PushEndpoint string  `json:"pushEndpoint"`
		ChannelID string     `json:"channelID"`
	}

	register := RegisterResponse{"register", 200, pushEndpoint, channelID}

	j, err := json.Marshal(register);
	if err != nil {
		log.Println("Could not convert hello response to json %s",err)
		return;
        }

	if err = websocket.Message.Send(client.Websocket, string(j)); err != nil {
		// we could not send the message to a peer
		log.Println("Could not send message to ", client.Websocket, err.Error())
	}
}

func handleHello(client *Client, f map[string]interface{}) {
	log.Println(" -> handleHello");

	status := 200;

	if (f["uaid"] == nil) {
		uaid, err := uuid.GenUUID()
		if  err != nil {
			status = 400;
			log.Println("GenUUID error %s",err)
		}
		client.UAID = uaid;
	} else {
		client.UAID = f["uaid"].(string)
	}

	if (f["interface"] != nil) {
		m := f["interface"].(map[string]interface{})
		client.Ip = m["ip"].(string);
		client.Port = m["port"].(float64);
	}

	type HelloResponse struct {
		Name string          `json:"messageType"`
		Status int           `json:"status"`
		UAID string          `json:"uaid"`
		Channels []*Channel  `json:"channelIDs"`
	}

	hello := HelloResponse{"hello", status, client.UAID, gUAIDToChannel[client.UAID]}

	j, err := json.Marshal(hello);
	if err != nil {
		log.Println("Could not convert hello response to json %s",err)
		return;
        }

	log.Println("going to send:  \n  ", string(j));
	if err = websocket.Message.Send(client.Websocket, string(j)); err != nil {
		log.Println("Could not send message to ", client.Websocket, err.Error())
	}
}

func handleAck(client *Client, f map[string]interface{}) {
	log.Println(" -> ack");
}

func pushHandler(ws *websocket.Conn) {

	log.Println("a");

	client := &Client{ws, "", "", 0}

	for {
		var f map[string]interface{}

		var err error
		if err = websocket.JSON.Receive(ws, &f); err != nil {
			log.Println("Websocket Disconnected.", err.Error())
			break;
		}

		log.Println("hi!");

		switch f["messageType"] {
		case "hello":
			handleHello(client, f);
			gConnectedClients[client.UAID] = client;
			break;

		case "register":
			handleRegister(client, f);
			break;

		case "ack":
			handleAck(client, f);
			break;
		default:
			log.Println(" -> Unknown", f);
			break;
		}
	}
	log.Println("Closing Websocket!");
	ws.Close();
	gConnectedClients[client.UAID].Websocket = nil;
}

func notifyHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method != "PUT" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Method must be PUT."))
		return
	}

	channelID := strings.Trim(r.URL.Path, APPSERVER_API_PREFIX)

	if (strings.Contains(channelID, "/") || len(channelID) != (32+4)) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Could not find a valid channelID."))
		return;
	}
	

	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Could not find a valid version."))
		return;
	}

	values := r.Form["version"]

	if (len(values) != 1) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Could not find one version."))
		return;
	}

	channel := gChannelIDToChannel[channelID];
	channel.Version = values[0];


	client := gConnectedClients[channel.uaid];
	if (client == nil) {
		log.Println("no known client for the channel.");
	} else if (client.Websocket == nil) {
		wakeupClient(client)
	} else {
		sendNotificationToClient(client, channel)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func wakeupClient(client *Client) {

	log.Println("wakeupClient: ", client);
	service := fmt.Sprintf("%s:%g", client.Ip, client.Port)

	udpAddr, err := net.ResolveUDPAddr("up4", service)
	if err != nil {
		log.Println("ResolveUDPAddr error ", err.Error())
		return
	}

	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		log.Println("DialUDP error ", err.Error())
		return
	}

	_, err = conn.Write([]byte("anything"))
	if err != nil {
		log.Println("UDP Write error ", err.Error())
		return
	}

}

func sendNotificationToClient(client *Client, channel *Channel)  {

	type NotificationResponse struct {
		Name string          `json:"messageType"`
		Channels []Channel   `json:"updates"`
	}

	var channels []Channel;
	channels = append(channels, *channel);

	notification := NotificationResponse{"notification",  channels}

	j, err := json.Marshal(notification);
	if err != nil {
		log.Println("Could not convert hello response to json %s",err)
		return;
        }

	log.Println("going to send:  \n  ", string(j));
	if err = websocket.Message.Send(client.Websocket, string(j)); err != nil {
		log.Println("Could not send message to ", channel, err.Error())
	}

}


func admin(w http.ResponseWriter, r *http.Request) {

	type User struct {
		UAID string
		Connected bool
		Channels []*Channel
	}

	type Arguments struct {
		PushEndpointPrefix   string
		Users []User
	}

// TODO https!
	arguments := Arguments{"http://" + HOST_NAME + ":" + PORT_NUMBER + APPSERVER_API_PREFIX, nil}

	for k := range gUAIDToChannel {
		connected := gConnectedClients[k] != nil;
		u := User{k, connected, gUAIDToChannel[k]};
		arguments.Users = append(arguments.Users, u);
	}

	t := template.New("users.template")
	s1, _ := t.ParseFiles("templates/users.template");
	s1.Execute(w, arguments)
}

func main() {

	gUAIDToChannel = make(map[string][]*Channel)
	gChannelIDToChannel = make(map[string]*Channel)

	gConnectedClients = make(map[string]*Client)

	http.Handle("/", http.FileServer(http.Dir(".")))

	http.HandleFunc("/admin", admin)

	http.Handle("/push", websocket.Handler(pushHandler))

	http.HandleFunc(APPSERVER_API_PREFIX, notifyHandler);

	log.Println("Listening on", HOST_NAME + ":" + PORT_NUMBER );
	log.Fatal(http.ListenAndServe(HOST_NAME  + ":" + PORT_NUMBER , nil))
}
