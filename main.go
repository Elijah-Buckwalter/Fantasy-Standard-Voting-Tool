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
	Password   string
	Tickets    int
	IsActive   bool      // True if a browser session is running this account
	LastActive time.Time // Tracked timestamp to manage heartbeat auto-timeouts
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
	ActiveSetCode string // Tracks the raw code (e.g. "neo") if it's an MTG Set

	// --- Custom Between-States Fields ---
	TransitionMode string    // "pre-talking", "pre-voting", "post-voting-success", "post-voting-fail", "terminated", "post-talking"
	TransitionEnd  time.Time // Pinpoint expiration for countdowns / results panels
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
    {{if .}}<p style="color:red; font-weight:bold;">{{.}}</p>{{end}}
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
    {{if .Error}}<p style="color:red; font-weight:bold;">{{.Error}}</p>{{end}}
    <hr>
    
    <h3>Create or Update User Account</h3>
    <form action="/master/save-user" method="POST">
        <input type="text" name="target_user" placeholder="Username (Required)" required>
        <input type="text" name="password" placeholder="Password (Required)" required>
        <input type="number" name="tickets" placeholder="Tickets (Blank = 0)">
        <button type="submit">Save Account</button>
    </form>
    
    <h3>Current Users Status</h3>
    <ul id="user-list">
        {{range $name, $user := .Users}}
            <li style="margin-bottom: 6px;">
                <strong>{{$name}}</strong>: {{$user.Tickets}} tickets remaining 
                {{if $user.IsActive}}
                    <span style="color: green; font-weight: bold; margin-left: 5px;">● Connected</span>
                {{else}}
                    <span style="color: #777; margin-left: 5px;">○ Offline</span>
                {{end}}
                <form action="/master/delete-user" method="POST" style="display:inline; margin-left:10px;">
                    <input type="hidden" name="target_user" value="{{$name}}">
                    <button type="submit" style="background: #991b1b; color: white; font-size: 0.8em; padding: 2px 6px;">Delete Account</button>
                </form>
            </li>
        {{end}}
    </ul>
    <hr>

    <h3>Generate QR Code For Users</h3>
    <div style="margin-bottom: 20px;">
        <button id="qr-portal-btn" style="background: #4A5568; color: white; padding: 10px 16px; border: 3px solid #000000; border-radius: 0px; cursor: pointer; font-weight: bold; font-size: 0.95rem; box-shadow: 4px 4px 0px #000000;">
            Generate Portal QR Code
        </button>
    </div>

    <script>
        document.getElementById('qr-portal-btn').addEventListener('click', function() {
            var targetUrl = 'https://unknotty-overstrong-atticus.ngrok-free.dev/'; 
            var qrApiUrl = 'https://api.qrserver.com/v1/create-qr-code/?size=300x300&data=' + encodeURIComponent(targetUrl);
            var varTab = window.open();
            varTab.document.write(
                '<!DOCTYPE html><html><head><title>Portal QR Code</title><meta name="viewport" content="width=device-width, initial-scale=1.0"><style>body { display: flex; flex-direction: column; align-items: center; justify-content: center; height: 100vh; margin: 0; font-family: system-ui, -apple-system, sans-serif; background: #F7FAFC;} .qr-card { text-align: center; padding: 24px; background: white; box-shadow: 0 4px 6px -1px rgba(0,0,0,0.1); border-radius: 8px;} img { max-width: 100%; height: auto; margin-bottom: 16px; display: block; } p { color: #2D3748; font-weight: 600; font-size: 1.1rem; margin: 0; word-break: break-all; }</style></head><body><div class="qr-card"><img src="' + qrApiUrl + '" alt="QR Code" width="300" height="300"><p>' + targetUrl + '</p></div></body></html>'
            );
            varTab.document.close();
        });
    </script>
    <hr>
    
    <h3>Launch Session</h3>
    <div style="background: #f4f4f4; padding: 15px; border-radius: 5px;">
        <form method="POST" style="display: inline;">
            <input type="text" name="symbol_name" placeholder="Symbol Name or 3-Letter MTG Set Code" required><br><br>
            <input type="number" name="duration" placeholder="Timer (Seconds)" required><br><br>
            
            <div style="border-top: 1px dashed #ccc; padding-top: 10px; margin-bottom: 10px;">
                <input type="number" name="target" placeholder="Ticket Threshold (Voting Only)"><br><br>
                <button type="submit" formaction="/master/start-voting" style="background: green; color: white;">Start VOTING Session</button>
                <button type="submit" formaction="/master/start-talking" style="background: blue; color: white;">Start TALKING Session</button>
            </div>
        </form>
        <form action="/master/kill-session" method="POST" style="display: inline;">
            <button type="submit" style="background: red; color: white;">Kill Active Session</button>
        </form>
    </div>

    <h3>Live Sessions Status</h3>
    <div id="live-sessions">
        {{if .TransitionMode}}
             <p>Transition Running: <strong style="color: purple;">{{.TransitionMode}}</strong> | Time Remaining: <strong>{{.TimeLeft}}s</strong></p>
        {{else if .VotingActive}}
            {{range $name, $sym := .Symbols}}
                <p>
                    Mode: <strong style="color:green;">VOTING</strong> | 
                    Topic/Set: <strong>{{$sym.Name}}</strong> | 
                    Timer: <span class="live-countdown" style="font-weight:bold; color:orange;">{{$.TimeLeft}}s remaining</span> | 
                    Status: {{if $sym.Reached}}<strong style="color:red;">❌ Threshold Closed Early</strong>{{else}}<strong>⏳ Open</strong>{{end}} |
                    Progress: [{{$sym.Votes}} / {{$sym.Target}}]
                    {{if $.ActiveSetCode}}<span style="color: purple; font-weight: bold; margin-left:10px;">🃏 (User-driven Scryfall Query Active: s:{{$.ActiveSetCode}})</span>{{end}}
                </p>
            {{end}}
        {{else if .TalkingActive}}
            {{range $name, $sym := .Symbols}}
                <p>
                    Mode: <strong style="color:blue;">TALKING (No Tickets)</strong> | 
                    Topic/Set: <strong>{{$sym.Name}}</strong> | 
                    Timer: <span class="live-countdown" style="font-weight:bold; color:orange;">{{$.TimeLeft}}s remaining</span> | 
                    Status: 🗣️ Discussion Active
                    {{if $.ActiveSetCode}}<span style="color: purple; font-weight: bold; margin-left:10px;">🃏 (User-driven Scryfall Query Active: s:{{$.ActiveSetCode}})</span>{{end}}
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
    <style>
        :root {
            --bg-body: #0f172a; --bg-card: #1e293b; --text-main: #e2e8f0;
            --text-muted: #94a3b8; --border: #334155; --ticket-bg: #1e293b;
            --card-scryfall: #1e293b; --search-border: purple;
        }
        :root.light-theme {
            --bg-body: #ffffff; --bg-card: #ffffff; --text-main: #000000;
            --text-muted: #4a5568; --border: #ccc; --ticket-bg: #eee;
            --card-scryfall: #fafafa; --search-border: purple;
        }
        
        body { background: var(--bg-body); color: var(--text-main); font-family: system-ui, sans-serif; padding: 20px; transition: background 0.2s, color 0.2s; }
        
        .header-container { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; margin-bottom: 10px; }
        .toggle-btn { background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border); padding: 6px 12px; border-radius: 4px; cursor: pointer; font-size: 0.85rem; font-weight: bold; }

        /* Animation Classes */
        .countdown-overlay {
            position: fixed;
            top: 0; left: 0; width: 100vw; height: 100vh;
            background: rgba(10, 15, 30, 0.95);
            display: flex; flex-direction: column;
            align-items: center; justify-content: center;
            z-index: 9999; color: white; text-align: center;
            font-family: system-ui, sans-serif;
            backdrop-filter: blur(8px);
        }
        .animate-pop {
            font-size: 5rem;
            font-weight: 900;
            margin: 20px 0;
            animation: pulseImpact 1s ease-out infinite;
        }
        .sub-header {
            font-size: 1.5rem;
            opacity: 0.8;
            letter-spacing: 1px;
            text-transform: uppercase;
        }
        @keyframes pulseImpact {
            0% { transform: scale(0.6); opacity: 0; }
            15% { transform: scale(1.1); opacity: 1; }
            30% { transform: scale(1.0); }
            80% { transform: scale(1.0); opacity: 1; }
            100% { transform: scale(1.4); opacity: 0; }
        }
    </style>
    <script>
        function initTheme() {
            const saved = localStorage.getItem('theme');
            if (saved === 'light') {
                document.documentElement.classList.add('light-theme');
            }
        }
        function toggleTheme() {
            const isLight = document.documentElement.classList.toggle('light-theme');
            localStorage.setItem('theme', isLight ? 'light' : 'dark');
            document.getElementById('theme-icon').innerText = isLight ? '🌙 Dark Mode' : '☀️ Light Mode';
        }
        initTheme();

        let globalTimeLeft = {{.TimeLeft}};
        let currentLoadedSetCode = "{{.ActiveSetCode}}";
        let debounceTimer;

        function fetchScryfallData(query) {
            const grid = document.getElementById("scryfall-live-grid");
            const statusText = document.getElementById("scryfall-status");
            if (!grid || !query.trim()) return;

            statusText.innerText = "Searching Scryfall...";

            fetch("https://api.scryfall.com/cards/search?q=" + encodeURIComponent(query))
                .then(res => {
                    if (!res.ok) throw new Error("No cards found or bad syntax");
                    return res.json();
                })
                .then(resData => {
                    statusText.innerText = "Showing matches for: " + query;
                    grid.innerHTML = ""; 

                    if (resData.data && resData.data.length > 0) {
                        resData.data.forEach(card => {
                            let imgURL = card.image_uris ? card.image_uris.normal : null;
                            if (!imgURL && card.card_faces && card.card_faces.length > 0) {
                                imgURL = card.card_faces[0].image_uris ? card.card_faces[0].image_uris.normal : null;
                            }

                            if (imgURL) {
                                const cardDiv = document.createElement("div");
                                cardDiv.style = "text-align: center; background: var(--card-scryfall); border: 1px solid var(--border); border-radius: 6px; padding: 6px;";
                                
                                const img = document.createElement("img");
                                img.src = imgURL;
                                img.alt = card.name;
                                img.style = "width: 100%; height: auto; border-radius: 4px; display: block; margin-bottom: 4px;";
                                
                                const span = document.createElement("span");
                                span.style = "font-size: 0.75rem; font-weight: 600; color: var(--text-muted); display: block; word-break: break-all;";
                                span.innerText = card.name;

                                cardDiv.appendChild(img);
                                cardDiv.appendChild(span);
                                grid.appendChild(cardDiv);
                            }
                        });
                    }
                })
                .catch(err => {
                    statusText.innerText = "No matching cards found inside Scryfall context.";
                    grid.innerHTML = "";
                });
        }

        function handleSearchInput(val) {
            clearTimeout(debounceTimer);
            debounceTimer = setTimeout(() => {
                fetchScryfallData(val);
            }, 450); 
        }

        function initSearchEngine() {
            const searchBar = document.getElementById("scryfall-search-input");
            if (searchBar) {
                fetchScryfallData(searchBar.value);
            }
        }

        setInterval(() => {
            let timerEl = document.getElementById("timer");
            let animatedTimerEl = document.getElementById("animated-timer");
            let sessionArea = document.getElementById("session-area");
            let currentMode = sessionArea ? sessionArea.getAttribute("data-session-state") : "none";

            if (globalTimeLeft > 0) {
                if (currentMode.startsWith("transition-")) {
                     if (timerEl) { timerEl.innerText = "Updating State..."; timerEl.style.color = "purple"; }
                     if (animatedTimerEl) { animatedTimerEl.innerText = globalTimeLeft + "s"; }
                } else {
                     if (timerEl) { timerEl.innerText = globalTimeLeft + "s remaining"; timerEl.style.color = "orange"; }
                }
                globalTimeLeft--;
            } else {
                if (currentMode === "voting") {
                    if (timerEl) { timerEl.innerText = "Voting Closed"; timerEl.style.color = "orange"; }
                } else if (currentMode === "talking") {
                    if (timerEl) { timerEl.innerText = "Session Closed"; timerEl.style.color = "orange"; }
                } else if (currentMode.startsWith("transition-")) {
                    if (timerEl) { timerEl.innerText = "Processing..."; timerEl.style.color = "purple"; }
                    if (animatedTimerEl) { 
                        if (currentMode === "transition-pre-talking" || currentMode === "transition-pre-voting") {
                            animatedTimerEl.innerText = "Begin";
                        } else {
                            animatedTimerEl.innerText = "0s";
                        }
                    }
                } else {
                    if (timerEl) { timerEl.innerText = "No Session Active"; timerEl.style.color = "red"; }
                }
            }
        }, 1000);

        setInterval(() => {
            fetch('/client?username={{.Username}}&ajax=true')
                .then(response => {
                    if (response.status === 401 || response.redirected) {
                        window.location.href = "/?error=Session+disconnected";
                        return;
                    }
                    return response.text();
                })
                .then(html => {
                    if(!html) return;
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

                        let newSetCode = newSessionArea.getAttribute("data-set-code");

                        if (currentMode !== newMode || currentLoadedSetCode !== newSetCode) {
                            currentLoadedSetCode = newSetCode;
                            currentSessionArea.setAttribute("data-session-state", newMode);
                            currentSessionArea.setAttribute("data-set-code", newSetCode);
                            currentSessionArea.innerHTML = newSessionArea.innerHTML;
                            currentSessionArea.setAttribute("data-time-left", serverTimeRaw);
                            
                            setTimeout(initSearchEngine, 50);
                        } else {
                            let currentForms = currentSessionArea.querySelectorAll(".interactive-node");
                            let newForms = newSessionArea.querySelectorAll(".interactive-node");
                            if (currentForms.length === newForms.length) {
                                for (let i = 0; i < currentForms.length; i++) {
                                    if (currentForms[i].innerHTML !== newForms[i].innerHTML) {
                                        currentForms[i].innerHTML = newForms[i].innerHTML;
                                    }
                                }
                            }
                        }
                    }
                });
        }, 1000);

        window.addEventListener("DOMContentLoaded", () => {
            initSearchEngine();
            if (document.documentElement.classList.contains('light-theme')) {
                document.getElementById('theme-icon').innerText = '🌙 Dark Mode';
            }
        });
    </script>
</head>
<body>
    <div class="header-container">
        <h2>Welcome, {{.Username}}</h2>
        <div>
            <button class="toggle-btn" onclick="toggleTheme()" id="theme-icon">☀️ Light Mode</button>
        </div>
    </div>
    <div style="background: var(--ticket-bg); padding:10px; display:inline-block; border: 1px solid var(--border);">
        Your Current Ticket Count: <strong id="ticket-count" style="font-size:1.5em; color:blue;">{{.Tickets}}</strong>
    </div>
    {{if .Error}}<p style="color:red; font-weight:bold;">{{.Error}}</p>{{end}}
    <hr style="border: 0; border-top: 1px solid var(--border);">
    
    {{if .TransitionMode}}
        <div id="session-area" data-session-state="transition-{{.TransitionMode}}" data-time-left="{{.TimeLeft}}" data-set-code="{{.ActiveSetCode}}">
             <div class="countdown-overlay">
                 {{if eq .TransitionMode "pre-talking"}}
                     <div class="sub-header" style="color: #63b3ed;">Discussion Session Approaching.</div>
                     <div id="animated-timer" class="animate-pop" style="color: #3182ce;">{{if eq .TimeLeft 0}}Begin{{else}}{{.TimeLeft}}s{{end}}</div>
                 {{else if eq .TransitionMode "pre-voting"}}
                     <div class="sub-header" style="color: #68d391;">Let the People's Tickets Be Heard!</div>
                     <div id="animated-timer" class="animate-pop" style="color: #38a169;">{{if eq .TimeLeft 0}}Begin{{else}}{{.TimeLeft}}s{{end}}</div>
                 {{else if eq .TransitionMode "post-voting-success"}}
                     <div class="sub-header" style="color: #fc8181;">Threshold Cleared</div>
                     <h1 style="margin:10px 0; font-size:2.5rem; max-width:80%; color:#e53e3e;">The Threshold Was Met, The Set Will Be Rerolled!</h1>
                     <div id="animated-timer" class="animate-pop" style="color: #e53e3e; font-size:3rem;">🛞</div>
                 {{else if eq .TransitionMode "post-voting-fail"}}
                     <div class="sub-header" style="color: #cbd5e0;">Time Expired</div>
                     <h1 style="margin:10px 0; font-size:2.5rem; max-width:80%; color:#a0aec0;">The Threshold Was Not Met, The Set Will Be Locked In.</h1>
                     <div id="animated-timer" class="animate-pop" style="color: #718096; font-size:3rem;">🔒</div>
                 {{else if eq .TransitionMode "terminated"}}
                     <div class="sub-header" style="color: #e53e3e;">System Alert</div>
                     <h1 style="margin:10px 0; font-size:2.5rem; max-width:80%; color:#fff;">Session Terminated Please Stand By.</h1>
                     <div id="animated-timer" class="animate-pop" style="color: #e53e3e; font-size:3rem;">🛑</div>
                 {{else if eq .TransitionMode "post-talking"}}
                     <div class="sub-header" style="color: #ed8936;">Discussion Closed</div>
                     <h1 style="margin:10px 0; font-size:2.5rem; max-width:80%; color:#edf2f7;">Times Up! Please Remain Quiet and Follow the Orators Instructions.</h1>
                     <div id="animated-timer" class="animate-pop" style="color: #ed8936; font-size:3rem;">⏳</div>
                 {{end}}
             </div>
        </div>
    {{else if .VotingActive}}
        <div id="session-area" data-session-state="voting" data-time-left="{{.TimeLeft}}" data-set-code="{{.ActiveSetCode}}">
            <h3>Active Session</h3>
            <p id="timer" style="font-size:1.2em; font-weight:bold; color:orange;">{{.TimeLeft}}s remaining</p>
            {{range $name, $sym := .Symbols}}
                <!-- Blocky Node with Heavy Border -->
                <div class="interactive-node" style="border: 3px solid #000000; padding: 10px; margin-bottom: 10px; background: var(--bg-card);">
                    
                    <!-- Inline Wrapper for Symbol Name and Threshold -->
                    <div style="display: flex; align-items: center; gap: 10px; margin-bottom: 12px; flex-wrap: wrap;">
                        <strong style="font-size: 1.1em; color: var(--text-main);">Symbol up for Vote: {{ $sym.Name }}</strong>
                        
                        <!-- Blocky Master Threshold Value Target Tag -->
                        <span style="background: #000000; color: #FFFFFF; padding: 2px 8px; font-size: 0.85rem; font-weight: bold; text-transform: uppercase; border-radius: 0px;">
                            Target: {{ $sym.Target }}
                        </span>
                    </div>

                    {{if $sym.Reached}}
                        <button disabled style="color: red; background: transparent; border: 2px dashed red; padding: 5px 15px; font-weight: bold; cursor: not-allowed;">
                            Threshold Closed
                        </button>
                    {{else}}
                        <form action="/client/vote" method="POST" style="display:inline;">
                            <input type="hidden" name="username" value="{{$.Username}}">
                            <input type="hidden" name="symbol" value="{{$sym.Name}}">
                            <!-- Blocky, sharp voting button -->
                            <button type="submit" style="background: green; color: white; padding: 5px 15px; border: 3px solid #000000; border-radius: 0px; font-weight: bold; cursor: pointer;">
                                Use 1 Ticket to Vote
                            </button>
                        </form>
                    {{end}}
                </div>
            {{end}}

            {{if .ActiveSetCode}}
                <div style="margin-top:20px; border-top: 2px solid var(--border); padding-top: 15px;">
                    <h4 style="color: purple; margin-bottom: 8px;">🃏 Filter Live Set Content:</h4>
                    <input type="text" id="scryfall-search-input" value="s:{{.ActiveSetCode}} " 
                           oninput="handleSearchInput(this.value)"
                           style="width: 100%; max-width: 500px; padding: 10px; font-size: 1rem; border: 2px solid var(--search-border); background: var(--bg-card); color: var(--text-main); border-radius: 4px; box-sizing: border-box;" 
                           placeholder="Filter e.g. s:{{.ActiveSetCode}} c:red r:rare">
                    <p id="scryfall-status" style="font-size: 0.85rem; font-weight: bold; color: var(--text-muted); margin: 6px 0 12px 0;">Initializing...</p>
                    <div id="scryfall-live-grid" style="display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 12px;"></div>
                </div>
            {{end}}
        </div>
    {{else if .TalkingActive}}
        <div id="session-area" data-session-state="talking" data-time-left="{{.TimeLeft}}" data-set-code="{{.ActiveSetCode}}">
            <h3>Active Session</h3>
            <p id="timer" style="font-size:1.2em; font-weight:bold; color:orange;">{{.TimeLeft}}s remaining</p>
            {{range $name, $sym := .Symbols}}
                <div class="interactive-node" style="border:1px solid blue; background: var(--card-scryfall); padding:15px; margin-bottom:5px; border-radius:5px;">
                    <strong style="font-size: 1.3em;">Currently Discussing: {{ $sym.Name }}</strong> 
                    <p style="color: var(--text-muted); margin-bottom:0;">🗣️ Talking session is open. No tickets required, voting is disabled.</p>
                </div>
            {{end}}

            {{if .ActiveSetCode}}
                <div style="margin-top:20px; border-top: 2px solid var(--border); padding-top: 15px;">
                    <h4 style="color: purple; margin-bottom: 8px;">🃏 Filter Live Set Content:</h4>
                    <input type="text" id="scryfall-search-input" value="s:{{.ActiveSetCode}} " 
                           oninput="handleSearchInput(this.value)"
                           style="width: 100%; max-width: 500px; padding: 10px; font-size: 1rem; border: 2px solid var(--search-border); background: var(--bg-card); color: var(--text-main); border-radius: 4px; box-sizing: border-box;" 
                           placeholder="Filter e.g. s:{{.ActiveSetCode}} c:red r:rare">
                    <p id="scryfall-status" style="font-size: 0.85rem; font-weight: bold; color: var(--text-muted); margin: 6px 0 12px 0;">Initializing...</p>
                    <div id="scryfall-live-grid" style="display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 12px;"></div>
                </div>
            {{end}}
        </div>
    {{else}}
        <div id="session-area" data-session-state="none" data-time-left="0" data-set-code="">
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

	go func() {
		for {
			time.Sleep(2 * time.Second)
			state.mu.Lock()
			for _, user := range state.Users {
				if user.IsActive && time.Since(user.LastActive) > 5*time.Second {
					user.IsActive = false
				}
			}
			state.mu.Unlock()
		}
	}()

	http.HandleFunc("/", loginPageHandler)
	http.HandleFunc("/login", loginHandler)
	http.HandleFunc("/master", masterDashboardHandler)
	http.HandleFunc("/master/save-user", saveUserHandler)
	http.HandleFunc("/master/delete-user", deleteUserHandler)
	http.HandleFunc("/master/start-voting", startVotingHandler)
	http.HandleFunc("/master/start-talking", startTalkingHandler)
	http.HandleFunc("/master/kill-session", killSessionHandler)
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

		if exists && user != nil {
			if subtle.ConstantTimeCompare([]byte(user.Password), []byte(password)) == 1 {
				if user.IsActive && time.Since(user.LastActive) <= 5*time.Second {
					state.mu.Unlock()
					http.Redirect(w, r, "/?error=This+account+is+already+logged+in+elsewhere.", http.StatusSeeOther)
					return
				}

				user.IsActive = true
				user.LastActive = time.Now()
				state.mu.Unlock()

				http.Redirect(w, r, "/client?username="+username, http.StatusSeeOther)
				return
			}
		}
		state.mu.Unlock()
	}
	http.Redirect(w, r, "/?error=Invalid+credentials", http.StatusSeeOther)
}

func checkAndArchiveExpiredSession() {
	now := time.Now()

	if state.TransitionMode != "" && now.After(state.TransitionEnd) {
		oldMode := state.TransitionMode
		state.TransitionMode = "" 

		if oldMode == "pre-talking" {
			state.TalkingActive = true
		} else if oldMode == "pre-voting" {
			state.VotingActive = true
		} else {
			state.VotingActive = false
			state.TalkingActive = false
			state.Symbols = make(map[string]*Symbol)
			state.ActiveSetCode = ""
		}
		return
	}

	if (state.VotingActive || state.TalkingActive) && now.After(state.TimerEnd) {
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

			state.TransitionMode = "post-voting-fail"
			state.TransitionEnd = now.Add(5 * time.Second) 
			state.TimerEnd = state.TransitionEnd
		} else if state.TalkingActive {
			for _, sym := range state.Symbols {
				symbolName = sym.Name
			}
			
			// Trigger talking session expiration transition screen
			state.TransitionMode = "post-talking"
			state.TransitionEnd = now.Add(5 * time.Second)
			state.TimerEnd = state.TransitionEnd
		}

		state.History = append([]HistoricalSession{{
			Mode:         modeName,
			Symbol:       symbolName,
			FinalTallies: detailText,
			Timestamp:    now.Format("15:04:05"),
		}}, state.History...)

		if state.TransitionMode == "" {
			state.VotingActive = false
			state.TalkingActive = false
		}
	}
}

func masterDashboardHandler(w http.ResponseWriter, r *http.Request) {
	state.mu.Lock()
	defer state.mu.Unlock()

	checkAndArchiveExpiredSession()

	var timeLeft int
	if state.TransitionMode != "" {
		timeLeft = int(time.Until(state.TransitionEnd).Seconds())
	} else {
		timeLeft = int(time.Until(state.TimerEnd).Seconds())
	}
	if timeLeft <= 0 {
		timeLeft = 0
	}

	data := struct {
		Users          map[string]*User
		Symbols        map[string]*Symbol
		History        []HistoricalSession
		VotingActive   bool
		TalkingActive  bool
		ActiveSetCode  string
		TimeLeft       int
		Error          string
		TransitionMode string
	}{
		Users:          state.Users,
		Symbols:        state.Symbols,
		History:        state.History,
		VotingActive:   state.VotingActive,
		TalkingActive:  state.TalkingActive,
		ActiveSetCode:  state.ActiveSetCode,
		TimeLeft:       timeLeft,
		Error:          r.URL.Query().Get("error"),
		TransitionMode: state.TransitionMode,
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

	user.LastActive = time.Now()
	user.IsActive = true

	checkAndArchiveExpiredSession()

	var timeLeft int
	if state.TransitionMode != "" {
		timeLeft = int(time.Until(state.TransitionEnd).Seconds())
	} else {
		timeLeft = int(time.Until(state.TimerEnd).Seconds())
	}
	if timeLeft <= 0 {
		timeLeft = 0
	}

	data := struct {
		Username       string
		Tickets        int
		Symbols        map[string]*Symbol
		VotingActive   bool
		TalkingActive  bool
		ActiveSetCode  string
		TimeLeft       int
		Error          string
		TransitionMode string
	}{
		Username:       username,
		Tickets:        user.Tickets,
		Symbols:        state.Symbols,
		VotingActive:   state.VotingActive,
		TalkingActive:  state.TalkingActive,
		ActiveSetCode:  state.ActiveSetCode,
		TimeLeft:       timeLeft,
		Error:          r.URL.Query().Get("error"),
		TransitionMode: state.TransitionMode,
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

	if !userExists || !symbolExists || !state.VotingActive || state.TransitionMode != "" {
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
		state.TransitionMode = "post-voting-success"
		state.TransitionEnd = time.Now().Add(5 * time.Second) 
		state.TimerEnd = state.TransitionEnd
	}

	http.Redirect(w, r, "/client?username="+username, http.StatusSeeOther)
}

func forceArchiveActiveSession() {
	if state.VotingActive || state.TalkingActive {
		modeName := "TALKING"
		detailText := "Killed manually by Master"
		symbolName := ""
		
		if state.VotingActive {
			modeName = "VOTING"
			for _, sym := range state.Symbols {
				symbolName = sym.Name
				detailText = fmt.Sprintf("Killed early. Votes caught: %d / %d", sym.Votes, sym.Target)
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
	
	// Trigger the "Session Terminated" overlay state for exactly 5 seconds
	state.TransitionMode = "terminated" 
	state.TransitionEnd = time.Now().Add(5 * time.Second)
	state.TimerEnd = state.TransitionEnd
	
	state.Symbols = make(map[string]*Symbol)
	state.ActiveSetCode = ""
}

func tryFetchScryfallData(inputString string) (string, string, bool) {
	cleanedCode := strings.TrimSpace(strings.ToLower(inputString))
	if len(cleanedCode) < 3 || len(cleanedCode) > 5 {
		return "", "", false 
	}

	client := &http.Client{Timeout: 4 * time.Second}
	
	setURL := fmt.Sprintf("https://api.scryfall.com/sets/%s", cleanedCode)
	req, _ := http.NewRequest("GET", setURL, nil)
	req.Header.Set("User-Agent", "MtgViewerPollingApp/1.0")
	req.Header.Set("Accept", "application/json")
	
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return "", "", false 
	}
	defer resp.Body.Close()

	var setMeta struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&setMeta); err != nil {
		return "", "", false
	}

	return setMeta.Name, cleanedCode, true
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

	displayName, parsedCode, isSet := tryFetchScryfallData(rawInput)
	if isSet {
		state.ActiveSetCode = parsedCode
		state.Symbols[displayName] = &Symbol{Name: displayName, Votes: 0, Target: target}
	} else {
		state.Symbols[rawInput] = &Symbol{Name: rawInput, Votes: 0, Target: target}
	}

	state.TransitionMode = "pre-voting"
	state.TransitionEnd = time.Now().Add(3 * time.Second)
	state.TimerEnd = state.TransitionEnd.Add(time.Duration(durationSec) * time.Second)

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

	displayName, parsedCode, isSet := tryFetchScryfallData(rawInput)
	if isSet {
		state.ActiveSetCode = parsedCode
		state.Symbols[displayName] = &Symbol{Name: displayName, Votes: 0, Target: 0}
	} else {
		state.Symbols[rawInput] = &Symbol{Name: rawInput, Votes: 0, Target: 0}
	}

	state.TransitionMode = "pre-talking"
	state.TransitionEnd = time.Now().Add(3 * time.Second)
	state.TimerEnd = state.TransitionEnd.Add(time.Duration(durationSec) * time.Second)

	http.Redirect(w, r, "/master", http.StatusSeeOther)
}

func killSessionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()

	forceArchiveActiveSession()

	http.Redirect(w, r, "/master", http.StatusSeeOther)
}

func saveUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()

	username := strings.TrimSpace(r.FormValue("target_user"))
	password := strings.TrimSpace(r.FormValue("password"))
	ticketsRaw := strings.TrimSpace(r.FormValue("tickets"))

	if username == "" || password == "" {
		http.Redirect(w, r, "/master?error=Username+and+Password+cannot+be+empty", http.StatusSeeOther)
		return
	}
	if username == "master" {
		http.Redirect(w, r, "/master?error=Cannot+override+system+master+account", http.StatusSeeOther)
		return
	}

	tickets := 0
	if ticketsRaw != "" {
		if val, err := strconv.Atoi(ticketsRaw); err == nil && val >= 0 {
			tickets = val
		}
	}

	isActive := false
	var lastActive time.Time
	if existing, exists := state.Users[username]; exists {
		isActive = existing.IsActive
		lastActive = existing.LastActive
	}

	state.Users[username] = &User{
		Password:   password,
		Tickets:    tickets,
		IsActive:   isActive,
		LastActive: lastActive,
	}

	http.Redirect(w, r, "/master", http.StatusSeeOther)
}

func deleteUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()

	targetUser := r.FormValue("target_user")
	delete(state.Users, targetUser)

	http.Redirect(w, r, "/master", http.StatusSeeOther)
}