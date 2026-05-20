package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// User represents a client account managed by the Master
type User struct {
	Password string
	Tickets  int
}

// Symbol represents an item up for voting or talking
type Symbol struct {
	Name    string
	Votes   int
	Target  int
	Reached bool
}

// HistoricalSession archives past events for the Master log
type HistoricalSession struct {
	Mode         string
	Symbol       string
	FinalTallies string
	Timestamp    string
}

// ScryfallCard represents the subset of card data we care about displaying
type ScryfallCard struct {
	Name     string `json:"name"`
	ImageURL string `json:"image_url"`
}

// ServerState is our single source of truth in memory
type ServerState struct {
	mu            sync.Mutex
	MasterPass    string
	Users         map[string]*User
	Symbols       map[string]*Symbol
	History       []HistoricalSession
	TimerEnd      time.Time
	VotingActive  bool
	TalkingActive bool
	ActiveSymName string

	// Ambient Card Data populated only if the active symbol is an MTG Set Code
	CurrentCards []ScryfallCard
}

var state = &ServerState{
	MasterPass:    "master123",
	Users:         make(map[string]*User),
	Symbols:       make(map[string]*Symbol),
	History:       make([]HistoricalSession, 0),
	VotingActive:  false,
	TalkingActive: false,
}

var templates = template.Must(template.New("all").Parse(`
{{define "login"}}
<!DOCTYPE html>
<html>
<head><title>Login</title><meta name="viewport" content="width=device-width, initial-scale=1.0"></head>
<body>
    <h2>System Login</h2>
    {{if .}}<p style="color:red;">{{.}}</p>{{end}}
    <form action="/login" method="POST">
        <input type="text" name="username" placeholder="Username" required><br><br>
        <input type="password" name="password" placeholder="Password" required><br><br>
        <button type="submit">Login</button>
    </form>
</body>
</html>
{{end}}

{{define "master"}}
<!DOCTYPE html>
<html>
<head>
    <title>Master Dashboard</title>
    <script>
        let time = {{.TimeLeft}};
        setInterval(() => {
            if(time > 0) {
                time--;
                let elements = document.getElementsByClassName("live-countdown");
                for(let el of elements) {
                    el.innerText = time + "s remaining";
                }
            } else {
                let elements = document.getElementsByClassName("live-countdown");
                for(let el of elements) {
                    el.innerText = "0s (Expired)";
                }
            }
        }, 1000);

        setInterval(() => {
            fetch('/master?ajax=true')
                .then(response => response.text())
                .then(html => {
                    let parser = new DOMParser();
                    let doc = parser.parseFromString(html, 'text/html');
                    
                    let userList = document.getElementById("user-list");
                    let newStaticUsers = doc.getElementById("user-list").innerHTML;
                    if (userList.innerHTML !== newStaticUsers) {
                        userList.innerHTML = newStaticUsers;
                    }
                    
                    let liveSessions = document.getElementById("live-sessions");
                    let newStaticSessions = doc.getElementById("live-sessions").innerHTML;
                    if (liveSessions.innerHTML !== newStaticSessions) {
                        liveSessions.innerHTML = newStaticSessions;
                    }

                    let historyLog = document.getElementById("history-log");
                    let newHistoryLog = doc.getElementById("history-log").innerHTML;
                    if (historyLog.innerHTML !== newHistoryLog) {
                        historyLog.innerHTML = newHistoryLog;
                    }
                });
        }, 1000);
    </script>
</head>
<body>
    <h1>Master Control Panel</h1>
    <hr>
    <h3>Manage User Tickets</h3>
    <form action="/master/update-tickets" method="POST">
        <input type="text" name="target_user" placeholder="Username" required>
        <input type="number" name="amount" placeholder="Tickets (+/-)" required>
        <button type="submit">Update Balance</button>
    </form>
    
    <h3>Current Users Status</h3>
    <ul id="user-list">
        {{range $name, $user := .Users}}
            <li><strong>{{$name}}</strong>: {{$user.Tickets}} tickets remaining</li>
        {{end}}
    </ul>
    <hr>

    <h3>Generate QR Code For Users</h3>
    <div style="margin-bottom: 20px;">
        <button id="qr-portal-btn" style="background: #4A5568; color: white; padding: 10px 16px; border: none; border-radius: 4px; cursor: pointer; font-weight: bold; font-size: 0.95rem;">
            📱 Generate Portal QR Code
        </button>
    </div>

    <script>
        document.getElementById('qr-portal-btn').addEventListener('click', function() {
            var targetUrl = 'https://unknotty-overstrong-atticus.ngrok-free.dev/'; 
            var qrApiUrl = 'https://api.qrserver.com/v1/create-qr-code/?size=300x300&data=' + encodeURIComponent(targetUrl);
            var newTab = window.open();
            newTab.document.write(
                '<!DOCTYPE html><html><head><title>Portal QR Code</title><meta name="viewport" content="width=device-width, initial-scale=1.0"><style>body { display: flex; flex-direction: column; align-items: center; justify-content: center; height: 100vh; margin: 0; font-family: system-ui, -apple-system, sans-serif; background: #F7FAFC;} .qr-card { text-align: center; padding: 24px; background: white; box-shadow: 0 4px 6px -1px rgba(0,0,0,0.1); border-radius: 8px;} img { max-width: 100%; height: auto; margin-bottom: 16px; display: block; } p { color: #2D3748; font-weight: 600; font-size: 1.1rem; margin: 0; word-break: break-all; }</style></head><body><div class="qr-card"><img src="' + qrApiUrl + '" alt="QR Code" width="300" height="300"><p>' + targetUrl + '</p></div></body></html>'
            );
            newTab.document.close();
        });
    </script>
    <hr>
    
    <h3>Launch Session</h3>
    <div style="background: #f4f4f4; padding: 15px; border-radius: 5px;">
        <form method="POST">
            <input type="text" name="symbol_name" placeholder="Symbol Name or 3-Letter MTG Set Code" required><br><br>
            <input type="number" name="duration" placeholder="Timer (Seconds)" required><br><br>
            
            <div style="border-top: 1px dashed #ccc; padding-top: 10px;">
                <input type="number" name="target" placeholder="Ticket Threshold (Voting Only)"><br><br>
                <button type="submit" formaction="/master/start-voting" style="background: green; color: white;">Start VOTING Session</button>
                <button type="submit" formaction="/master/start-talking" style="background: blue; color: white;">Start TALKING Session</button>
            </div>
        </form>
    </div>

    <h3>Live Sessions Status</h3>
    <div id="live-sessions">
        {{if .VotingActive}}
            {{range $name, $sym := .Symbols}}
                <p>
                    Mode: <strong style="color:green;">VOTING</strong> | 
                    Topic/Set: <strong>{{$sym.Name}}</strong> | 
                    Timer: <span class="live-countdown" style="font-weight:bold; color:orange;">{{$.TimeLeft}}s remaining</span> | 
                    Status: {{if $sym.Reached}}<strong style="color:red;">❌ Threshold Closed Early</strong>{{else}}<strong>⏳ Open</strong>{{end}} |
                    Progress: [{{$sym.Votes}} / {{$sym.Target}}]
                    {{if $.CurrentCards}}<span style="color: purple; font-weight: bold; margin-left:10px;">🃏 (MTG Card Grid Overlay Active)</span>{{end}}
                </p>
            {{end}}
        {{else if .TalkingActive}}
            {{range $name, $sym := .Symbols}}
                <p>
                    Mode: <strong style="color:blue;">TALKING (No Tickets)</strong> | 
                    Topic/Set: <strong>{{$sym.Name}}</strong> | 
                    Timer: <span class="live-countdown" style="font-weight:bold; color:orange;">{{$.TimeLeft}}s remaining</span> | 
                    Status: 🗣️ Discussion Active
                    {{if $.CurrentCards}}<span style="color: purple; font-weight: bold; margin-left:10px;">🃏 (MTG Card Grid Overlay Active)</span>{{end}}
                </p>
            {{end}}
        {{else}}
            <p>No session is currently running.</p>
        {{end}}
    </div>

    <hr>
    <h3>Prior Sessions</h3>
    <div id="history-log" style="background: #fafafa; padding: 10px; border: 1px solid #ddd; max-height: 200px; overflow-y: auto;">
        {{range .History}}
            <p style="margin: 5px 0; border-bottom: 1px solid #eee; padding-bottom: 5px;">
                [{{.Timestamp}}] Mode: <strong>{{.Mode}}</strong> | Symbol: <strong>{{.Symbol}}</strong> | Details: <em>{{.FinalTallies}}</em>
            </p>
        {{else}}
            <p style="color: #777;">No prior sessions recorded yet.</p>
        {{end}}
    </div>
</body>
</html>
{{end}}

{{define "client"}}
<!DOCTYPE html>
<html>
<head>
    <title>Voting Portal</title>
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <script>
        let globalTimeLeft = {{.TimeLeft}};

        setInterval(() => {
            let timerEl = document.getElementById("timer");
            if (!timerEl) return;

            let sessionArea = document.getElementById("session-area");
            let currentMode = sessionArea ? sessionArea.getAttribute("data-session-state") : "none";

            if (globalTimeLeft > 0) {
                timerEl.innerText = globalTimeLeft + "s remaining";
                timerEl.style.color = "orange";
                globalTimeLeft--;
            } else {
                if (currentMode === "voting") {
                    timerEl.innerText = "Voting Closed";
                    timerEl.style.color = "orange";
                } else if (currentMode === "talking") {
                    timerEl.innerText = "Session Closed";
                    timerEl.style.color = "orange";
                } else {
                    timerEl.innerText = "No Session Active";
                    timerEl.style.color = "red";
                }
            }
        }, 1000);

        setInterval(() => {
            fetch('/client?username={{.Username}}&ajax=true')
                .then(response => response.text())
                .then(html => {
                    let parser = new DOMParser();
                    let doc = parser.parseFromString(html, 'text/html');
                    
                    let currentCount = document.getElementById("ticket-count");
                    let newCount = doc.getElementById("ticket-count");
                    if (currentCount && newCount && currentCount.innerText !== newCount.innerText) {
                        currentCount.innerText = newCount.innerText;
                    }
                    
                    let currentSessionArea = document.getElementById("session-area");
                    let newSessionArea = doc.getElementById("session-area");
                    
                    if (currentSessionArea && newSessionArea) {
                        let currentMode = currentSessionArea.getAttribute("data-session-state");
                        let newMode = newSessionArea.getAttribute("data-session-state");
                        
                        let serverTimeRaw = newSessionArea.getAttribute("data-time-left");
                        if (serverTimeRaw) {
                            let parsedServerTime = parseInt(serverTimeRaw);
                            if (Math.abs(globalTimeLeft - parsedServerTime) > 1 || parsedServerTime === 0) {
                                globalTimeLeft = parsedServerTime;
                            }
                        }

                        if (currentMode !== newMode) {
                            currentSessionArea.setAttribute("data-session-state", newMode);
                            currentSessionArea.innerHTML = newSessionArea.innerHTML;
                            currentSessionArea.setAttribute("data-time-left", serverTimeRaw);
                        } else {
                            // Sync operational pieces
                            let currentForms = currentSessionArea.querySelectorAll(".interactive-node");
                            let newForms = newSessionArea.querySelectorAll(".interactive-node");
                            if (currentForms.length === newForms.length) {
                                for (let i = 0; i < currentForms.length; i++) {
                                    if (currentForms[i].innerHTML !== newForms[i].innerHTML) {
                                        currentForms[i].innerHTML = newForms[i].innerHTML;
                                    }
                                }
                            }
                            // Also keep images synced seamlessly if there was a slight backend payload adjustment
                            let currentGallery = currentSessionArea.querySelector(".card-gallery-grid");
                            let newGallery = newSessionArea.querySelector(".card-gallery-grid");
                            if (currentGallery && newGallery && currentGallery.innerHTML !== newGallery.innerHTML) {
                                currentGallery.innerHTML = newGallery.innerHTML;
                            }
                        }
                    }
                });
        }, 1000);
    </script>
</head>
<body>
    <h2>Welcome, {{.Username}}</h2>
    <div style="background:#eee; padding:10px; display:inline-block;">
        Your Current Ticket Count: <strong id="ticket-count" style="font-size:1.5em; color:blue;">{{.Tickets}}</strong>
    </div>
    {{if .Error}}<p style="color:red; font-weight:bold;">{{.Error}}</p>{{end}}
    <hr>
    
    {{if .VotingActive}}
        <div id="session-area" data-session-state="voting" data-time-left="{{.TimeLeft}}">
            <h3>Active Session</h3>
            <p id="timer" style="font-size:1.2em; font-weight:bold; color:orange;">{{.TimeLeft}}s remaining</p>
            {{range $name, $sym := .Symbols}}
                <div class="interactive-node" style="border:1px solid #ccc; padding:10px; margin-bottom:5px;">
                    <strong>Symbol up for Vote: {{ $sym.Name }}</strong> 
                    <br><br>
                    {{if $sym.Reached}}
                        <button disabled style="color:red;">Threshold Closed</button>
                    {{else}}
                        <form action="/client/vote" method="POST" style="display:inline;">
                            <input type="hidden" name="username" value="{{$.Username}}">
                            <input type="hidden" name="symbol" value="{{$sym.Name}}">
                            <button type="submit" style="background:green; color:white; padding:5px 15px;">Use 1 Ticket to Vote</button>
                        </form>
                    {{end}}
                </div>
            {{end}}

            {{/* Embedded Card Injector */}}
            {{if .CurrentCards}}
                <h4 style="color: purple; margin-top:20px; border-top: 2px solid #ccc; padding-top: 15px;">🃏 Associated Magic Cards for Reference:</h4>
                <div class="card-gallery-grid" style="display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 12px;">
                    {{range .CurrentCards}}
                        <div style="text-align: center; background: #fafafa; border: 1px solid #e2e8f0; border-radius: 6px; padding: 6px;">
                            <img src="{{.ImageURL}}" alt="{{.Name}}" style="width: 100%; height: auto; border-radius: 4px; display: block; margin-bottom: 4px;">
                            <span style="font-size: 0.75rem; font-weight: 600; color: #4a5568; display: block; word-break: break-all;">{{.Name}}</span>
                        </div>
                    {{end}}
                </div>
            {{end}}
        </div>
    {{else if .TalkingActive}}
        <div id="session-area" data-session-state="talking" data-time-left="{{.TimeLeft}}">
            <h3>Active Session</h3>
            <p id="timer" style="font-size:1.2em; font-weight:bold; color:orange;">{{.TimeLeft}}s remaining</p>
            {{range $name, $sym := .Symbols}}
                <div class="interactive-node" style="border:1px solid blue; background: #eef5ff; padding:15px; margin-bottom:5px; border-radius:5px;">
                    <strong style="font-size: 1.3em;">Currently Discussing: {{ $sym.Name }}</strong> 
                    <p style="color: #555; margin-bottom:0;">🗣️ Talking session is open. No tickets required, voting is disabled.</p>
                </div>
            {{end}}

            {{/* Embedded Card Injector */}}
            {{if .CurrentCards}}
                <h4 style="color: purple; margin-top:20px; border-top: 2px solid #ccc; padding-top: 15px;">🃏 Associated Magic Cards for Reference:</h4>
                <div class="card-gallery-grid" style="display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 12px;">
                    {{range .CurrentCards}}
                        <div style="text-align: center; background: #fafafa; border: 1px solid #e2e8f0; border-radius: 6px; padding: 6px;">
                            <img src="{{.ImageURL}}" alt="{{.Name}}" style="width: 100%; height: auto; border-radius: 4px; display: block; margin-bottom: 4px;">
                            <span style="font-size: 0.75rem; font-weight: 600; color: #4a5568; display: block; word-break: break-all;">{{.Name}}</span>
                        </div>
                    {{end}}
                </div>
            {{end}}
        </div>
    {{else}}
        <div id="session-area" data-session-state="none" data-time-left="0">
            <h3>Active Session</h3>
            <p id="timer" style="font-size:1.2em; font-weight:bold; color:red;">No Session Active</p>
            <p>No active voting or talking session at this moment.</p>
        </div>
    {{end}}
</body>
</html>
{{end}}
`))

func main() {
	state.Users["user1"] = &User{Password: "password123", Tickets: 10}
	state.Users["user2"] = &User{Password: "password456", Tickets: 5}

	http.HandleFunc("/", loginPageHandler)
	http.HandleFunc("/login", loginHandler)
	http.HandleFunc("/master", masterDashboardHandler)
	http.HandleFunc("/master/update-tickets", updateTicketsHandler)
	http.HandleFunc("/master/start-voting", startVotingHandler)
	http.HandleFunc("/master/start-talking", startTalkingHandler)
	http.HandleFunc("/client", clientDashboardHandler)
	http.HandleFunc("/client/vote", voteHandler)

	fmt.Println("Server starting on http://localhost:8080")
	if err := http.ListenAndServe("0.0.0.0:8080", nil); err != nil {
		fmt.Printf("Server failed to start: %v\n", err)
	}
}

func loginPageHandler(w http.ResponseWriter, r *http.Request) {
	templates.ExecuteTemplate(w, "login", r.URL.Query().Get("error"))
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")

	if username == "master" {
		if subtle.ConstantTimeCompare([]byte(password), []byte(state.MasterPass)) == 1 {
			http.Redirect(w, r, "/master", http.StatusSeeOther)
			return
		}
	} else {
		state.mu.Lock()
		user, exists := state.Users[username]
		state.mu.Unlock()

		if exists && user != nil {
			if subtle.ConstantTimeCompare([]byte(user.Password), []byte(password)) == 1 {
				http.Redirect(w, r, "/client?username="+username, http.StatusSeeOther)
				return
			}
		}
	}
	http.Redirect(w, r, "/?error=Invalid+credentials", http.StatusSeeOther)
}

func checkAndArchiveExpiredSession() {
	if (state.VotingActive || state.TalkingActive) && time.Now().After(state.TimerEnd) {
		modeName := "TALKING"
		detailText := "Discussion completed cleanly"
		symbolName := ""

		if state.VotingActive {
			modeName = "VOTING"
			detailText = "Voting period expired"
			for _, sym := range state.Symbols {
				symbolName = sym.Name
				detailText = fmt.Sprintf("Final Votes: %d (Target: %d)", sym.Votes, sym.Target)
				if sym.Reached {
					detailText += " [Threshold Met]"
				}
			}
		} else if state.TalkingActive {
			for _, sym := range state.Symbols {
				symbolName = sym.Name
			}
		}

		state.History = append([]HistoricalSession{{
			Mode:         modeName,
			Symbol:       symbolName,
			FinalTallies: detailText,
			Timestamp:    time.Now().Format("15:04:05"),
		}}, state.History...)

		state.VotingActive = false
		state.TalkingActive = false
		state.Symbols = make(map[string]*Symbol)
		state.CurrentCards = nil
	}
}

func masterDashboardHandler(w http.ResponseWriter, r *http.Request) {
	state.mu.Lock()
	defer state.mu.Unlock()

	checkAndArchiveExpiredSession()

	timeLeft := int(time.Until(state.TimerEnd).Seconds())
	if timeLeft <= 0 {
		timeLeft = 0
	}

	data := struct {
		Users         map[string]*User
		Symbols       map[string]*Symbol
		History       []HistoricalSession
		VotingActive  bool
		TalkingActive bool
		CurrentCards  []ScryfallCard
		TimeLeft      int
		Error         string
	}{
		Users:         state.Users,
		Symbols:       state.Symbols,
		History:       state.History,
		VotingActive:  state.VotingActive,
		TalkingActive: state.TalkingActive,
		CurrentCards:  state.CurrentCards,
		TimeLeft:      timeLeft,
		Error:         r.URL.Query().Get("error"),
	}

	templates.ExecuteTemplate(w, "master", data)
}

func clientDashboardHandler(w http.ResponseWriter, r *http.Request) {
	state.mu.Lock()
	username := r.URL.Query().Get("username")
	user, exists := state.Users[username]
	
	if !exists || user == nil {
		state.mu.Unlock()
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	checkAndArchiveExpiredSession()

	timeLeft := int(time.Until(state.TimerEnd).Seconds())
	if timeLeft <= 0 {
		timeLeft = 0
	}

	data := struct {
		Username      string
		Tickets       int
		Symbols       map[string]*Symbol
		VotingActive  bool
		TalkingActive bool
		CurrentCards  []ScryfallCard
		TimeLeft      int
		Error         string
	}{
		Username:      username,
		Tickets:       user.Tickets,
		Symbols:       state.Symbols,
		VotingActive:  state.VotingActive,
		TalkingActive: state.TalkingActive,
		CurrentCards:  state.CurrentCards,
		TimeLeft:      timeLeft,
		Error:         r.URL.Query().Get("error"),
	}
	state.mu.Unlock()

	templates.ExecuteTemplate(w, "client", data)
}

func voteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}
	username := r.FormValue("username")
	symbolName := r.FormValue("symbol")

	state.mu.Lock()
	defer state.mu.Unlock()

	if time.Now().After(state.TimerEnd) {
		checkAndArchiveExpiredSession()
	}

	user, userExists := state.Users[username]
	symbol, symbolExists := state.Symbols[symbolName]

	if !userExists || !symbolExists || !state.VotingActive {
		http.Redirect(w, r, "/client?username="+username+"&error=Voting+is+inactive", http.StatusSeeOther)
		return
	}

	if symbol.Reached {
		http.Redirect(w, r, "/client?username="+username+"&error=Threshold+reached!+Ticket+returned.", http.StatusSeeOther)
		return
	}

	if user.Tickets <= 0 {
		http.Redirect(w, r, "/client?username="+username+"&error=No+tickets", http.StatusSeeOther)
		return
	}

	user.Tickets--
	symbol.Votes++

	if symbol.Votes >= symbol.Target {
		symbol.Reached = true
		state.History = append([]HistoricalSession{{
			Mode:         "VOTING",
			Symbol:       symbol.Name,
			FinalTallies: fmt.Sprintf("Threshold Met Early! Total Votes: %d (Target: %d)", symbol.Votes, symbol.Target),
			Timestamp:    time.Now().Format("15:04:05"),
		}}, state.History...)
		state.VotingActive = false
		state.Symbols = make(map[string]*Symbol)
		state.CurrentCards = nil
	}

	http.Redirect(w, r, "/client?username="+username, http.StatusSeeOther)
}

func forceArchiveActiveSession() {
	if state.VotingActive || state.TalkingActive {
		modeName := "TALKING"
		detailText := "Interrupted/Closed by Master"
		symbolName := ""
		
		if state.VotingActive {
			modeName = "VOTING"
			for _, sym := range state.Symbols {
				symbolName = sym.Name
				detailText = fmt.Sprintf("Interrupted early. Votes caught: %d / %d", sym.Votes, sym.Target)
			}
		} else if state.TalkingActive {
			for _, sym := range state.Symbols {
				symbolName = sym.Name
			}
		}

		state.History = append([]HistoricalSession{{
			Mode:         modeName,
			Symbol:       symbolName,
			FinalTallies: detailText,
			Timestamp:    time.Now().Format("15:04:05"),
		}}, state.History...)
	}
	
	state.VotingActive = false
	state.TalkingActive = false
	state.Symbols = make(map[string]*Symbol)
	state.CurrentCards = nil
}

// tryFetchScryfallData attempts to retrieve cards if input matching a real MTG set code is found.
// It returns (Full Set Name, Cards Slice, true) on success, or fallback errors on failure.
func tryFetchScryfallData(inputString string) (string, []ScryfallCard, bool) {
	cleanedCode := strings.TrimSpace(strings.ToLower(inputString))
	if len(cleanedCode) < 3 || len(cleanedCode) > 5 {
		return "", nil, false // Optimization: skip hitting API if layout is definitely a custom phrase
	}

	client := &http.Client{Timeout: 5 * time.Second}
	
	// Check set metadata presence
	setURL := fmt.Sprintf("https://api.scryfall.com/sets/%s", cleanedCode)
	req, _ := http.NewRequest("GET", setURL, nil)
	req.Header.Set("User-Agent", "MtgViewerPollingApp/1.0")
	req.Header.Set("Accept", "application/json")
	
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return "", nil, false // Silent fallback: Treat input as plain text symbol topic
	}
	defer resp.Body.Close()

	var setMeta struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&setMeta); err != nil {
		return "", nil, false
	}

	// Fetch cards belonging to matching set query
	searchURL := fmt.Sprintf("https://api.scryfall.com/cards/search?q=s:%s", cleanedCode)
	reqSearch, _ := http.NewRequest("GET", searchURL, nil)
	reqSearch.Header.Set("User-Agent", "MtgViewerPollingApp/1.0")
	reqSearch.Header.Set("Accept", "application/json")

	respSearch, err := client.Do(reqSearch)
	if err != nil || respSearch.StatusCode != http.StatusOK {
		return setMeta.Name, nil, true // Return set text context even if card arrays parse dry
	}
	defer respSearch.Body.Close()

	var scryfallResponse struct {
		Data []struct {
			Name       string `json:"name"`
			ImageUris  struct {
				Normal string `json:"normal"`
			} `json:"image_uris"`
			CardFaces []struct {
				Name      string `json:"name"`
				ImageUris struct {
					Normal string `json:"normal"`
				} `json:"image_uris"`
			} `json:"card_faces"`
		} `json:"data"`
	}

	if err := json.NewDecoder(respSearch.Body).Decode(&scryfallResponse); err != nil {
		return setMeta.Name, nil, true
	}

	var parsedCards []ScryfallCard
	for _, rawCard := range scryfallResponse.Data {
		imgURL := rawCard.ImageUris.Normal
		if imgURL == "" && len(rawCard.CardFaces) > 0 {
			imgURL = rawCard.CardFaces[0].ImageUris.Normal
		}
		if imgURL != "" {
			parsedCards = append(parsedCards, ScryfallCard{
				Name:     rawCard.Name,
				ImageURL: imgURL,
			})
		}
	}

	return setMeta.Name, parsedCards, true
}

func startVotingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()

	forceArchiveActiveSession()

	rawInput := r.FormValue("symbol_name")
	target, _ := strconv.Atoi(r.FormValue("target"))
	durationSec, _ := strconv.Atoi(r.FormValue("duration"))

	// Check for card context completely inline
	displayName, cards, isSet := tryFetchScryfallData(rawInput)
	if isSet {
		state.CurrentCards = cards
		state.Symbols[displayName] = &Symbol{Name: displayName, Votes: 0, Target: target}
	} else {
		state.Symbols[rawInput] = &Symbol{Name: rawInput, Votes: 0, Target: target}
	}

	state.TimerEnd = time.Now().Add(time.Duration(durationSec) * time.Second)
	state.VotingActive = true

	http.Redirect(w, r, "/master", http.StatusSeeOther)
}

func startTalkingHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()

	forceArchiveActiveSession()

	rawInput := r.FormValue("symbol_name")
	durationSec, _ := strconv.Atoi(r.FormValue("duration"))

	// Check for card context completely inline
	displayName, cards, isSet := tryFetchScryfallData(rawInput)
	if isSet {
		state.CurrentCards = cards
		state.Symbols[displayName] = &Symbol{Name: displayName, Votes: 0, Target: 0}
	} else {
		state.Symbols[rawInput] = &Symbol{Name: rawInput, Votes: 0, Target: 0}
	}

	state.TimerEnd = time.Now().Add(time.Duration(durationSec) * time.Second)
	state.TalkingActive = true

	http.Redirect(w, r, "/master", http.StatusSeeOther)
}

func updateTicketsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()

	targetUser := r.FormValue("target_user")
	amount, _ := strconv.Atoi(r.FormValue("amount"))

	if _, exists := state.Users[targetUser]; !exists {
		state.Users[targetUser] = &User{Password: "123", Tickets: 0}
	}

	state.Users[targetUser].Tickets += amount
	if state.Users[targetUser].Tickets < 0 {
		state.Users[targetUser].Tickets = 0
	}

	http.Redirect(w, r, "/master", http.StatusSeeOther)
}