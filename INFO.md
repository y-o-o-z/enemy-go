# enemy-go — instrukcja obsługi

Test-clones harness do IRCnetu. Rewrite C-owej `enemy` (fahren / Pojeby Team)
w Go z biblioteką `github.com/kofany/go-ircevo`. Lista serwerów ściągana
automatycznie z `https://bot.ircnet.info/api/v2/serversByCountry`
(filtruje tylko `open: true`, resolve A/AAAA równolegle).

---

## Lokalizacja

Na love.ircnet.pl (port SSH 33911, login `y`):

| ścieżka                       | co to                                          |
|-------------------------------|------------------------------------------------|
| `/usr/local/bin/enemy`        | binarka, world-readable, odpalasz z usera      |
| `~y/enemy-go-src/`            | źródła Go (main.go, clone.go, manager.go, …)   |
| `~y/enemy-go-src.tgz`         | tar źródeł                                     |

`/root/enemy*` zostało usunięte — wszystko żyje w PATH-cie.

---

## Uruchamianie

Zwykły user (NIE sudo).

### Tryb interaktywny (kreator startowy)

Odpal bez argumentów:

```bash
enemy
```

Kreator pyta po kolei:

1. **Address family** — `ipv4`, `ipv6` albo `both` (default `both`).
2. **Bind IPs** — na które lokalne adresy klony mają się bindować.
   Można wpisać `all`, listę indeksów (`1,3`) albo same adresy
   (`203.0.113.5,2a01:6ee0::1`).
3. **Liczba klonów** — ilu spawnować od razu (default 1).
4. **Kanały** — opcjonalnie, CSV. Każdy klon zawoła `JOIN` po 001.

Mode jest **wyprowadzany z wybranych IP-ków**: tylko v4 → `ipv4`,
tylko v6 → `ipv6`, mieszane → `both`. Czyli klon nigdy nie spróbuje
się połączyć po rodzinie której nie ma w puli.

Bypass kreatora: dowolny z `-n / -bind-v4 / -bind-v6` albo
`-no-wizard`.

### Tryb skryptowy (flagi)

```bash
enemy -mode both -n 5 -channels '#test'
```

Po starcie binarka:

1. ściąga listę otwartych serwerów IRCnet,
2. resolwuje je do A/AAAA,
3. wykrywa lokalne IP (`v4=1 v6=23` na love),
4. spawnuje `N` klonów (round-robin po serwerach, alternacja v4/v6 dla `mode=both`),
5. wpada w interaktywny shell `enemy>` z menu komend.

### Najczęstsze flagi

| flaga                    | znaczenie                                                               | default                |
|--------------------------|-------------------------------------------------------------------------|------------------------|
| `-mode ipv4\|ipv6\|both` | rodzina adresów do bindowania                                           | `both`                 |
| `-n N`                   | ile klonów spawnować na starcie (`0` = sam shell)                       | `0`                    |
| `-channels '#a,#b'`      | kanały, do których każdy klon dołącza po 001                            | (brak)                 |
| `-stagger 1500ms`        | odstęp między kolejnymi connectami przy spawnie                          | `250ms`                |
| `-port 6667`             | port IRC                                                                | `6667`                 |
| `-bind-v4 'IP1,IP2'`     | wymuszenie konkretnych źródłowych IPv4 (CSV)                            | auto-detect z `eth0`   |
| `-bind-v6 'IP1,IP2'`     | jw. dla IPv6                                                            | auto-detect z `eth0`   |
| `-servers 'h1,h2'`       | pomija registry, używa tych hostów                                       | (registry ircnet.info) |
| `-server-list URL`       | inny endpoint listy serwerów                                            | bot.ircnet.info v2     |
| `-realnames /path`       | plik z realname'ami (1 linia = 1 wpis, `#` to komentarz)                | wbudowane              |
| `-reasons /path`         | jw. dla quit-reasonów                                                   | wbudowane              |
| `-kick-reasons /path`    | jw. dla kick-reasonów                                                   | wbudowane              |
| `-list-servers`          | tylko wypisuje ściągniętą listę i wychodzi                              | —                      |
| `-i`                     | wymusza kreator startowy (interaktywne pytania)                         | —                      |
| `-no-wizard`             | wyłącza auto-kreator nawet na TTY                                       | —                      |
| `-version`               | pokazuje wersję                                                          | —                      |

### Przykłady

```bash
# 5 klonów, IPv6, na #polska
enemy -mode ipv6 -n 5 -channels '#polska'

# 10 klonów, IPv4 only, jeden konkretny adres źródłowy
enemy -mode ipv4 -n 10 -bind-v4 79.139.59.33

# tylko podgląd dostępnych serwerów (open=true)
enemy -list-servers -mode both

# uruchomienie ograniczone do dwóch hostów
enemy -mode both -n 4 -servers 'irc.spadhausen.com,openirc.snt.utwente.nl'

# własna lista kick-reasonów z pliku
enemy -mode ipv6 -n 3 -kick-reasons /home/y/my-reasons.txt
```

---

## Zamykanie

W shellu `enemy>`:

| komenda             | efekt                                                            |
|---------------------|------------------------------------------------------------------|
| `exit` / `quit` / `q` | wszystkie klony QUIT-ują z losowym reasonem, proces wychodzi    |
| `disco`             | wszystkie QUIT-ują, ale shell zostaje (możesz znowu `load`-ować) |
| `del <id>`          | zabija pojedynczego klona po ID (`stat` daje listę ID)           |
| Ctrl-C / SIGTERM    | sygnał: 8s na czysty QUIT każdego klona, potem `os.Exit(0)`      |

Klony zawsze zamykają się czystym `QUIT :<reason>` (random z listy
quit-reasonów — generic disconnect strings, żeby wyglądały jak realni
userzy wypadający z sieci).

---

## Dorzucanie kolejnych klonów

Są dwie ścieżki — wybierz w zależności od tego, czy chcesz mieć
**jeden proces** czy **kilka osobnych instancji**.

### A) Dorzucenie w działającym procesie

Najszybsza droga. Z poziomu shella `enemy>`:

```
load 5
```

Spawnuje 5 nowych klonów. Wszyscy automatycznie używają tej samej
listy serwerów (round-robin po niej), tej samej puli lokalnych IP
i aktualnie ustawionego `mode`. Jak chcesz w trakcie zmienić tryb
dla *następnych* klonów (istniejące zostają nienaruszone):

```
ipmode ipv4
load 3
```

Kanały dorzucone w trakcie przez `join #X` są dziedziczone przez
przyszłe klony (są dopisywane do listy auto-join).

### B) Druga instancja na innych serwerach

Jeśli chcesz odseparować ruch (np. instancja A na hostach holenderskich,
instancja B na węgierskich) — odpal drugi proces w innym terminalu /
tmux-ie z flagą `-servers`:

```bash
# tmux 1:
enemy -mode both -n 5 -servers 'ircnet.tngnet.nl,openirc.snt.utwente.nl,hostsailor.ircnet.nl'

# tmux 2:
enemy -mode both -n 5 -servers 'irc.atw-inter.net,ssl.irc.atw-inter.net'
```

Każda instancja ma własne IP-pool counter, własny shell. Nie
komunikują się ze sobą — z punktu widzenia sieci to po prostu
osobne grupy klonów.

> Aktualną listę open-serwerów w danym momencie zobaczysz przez
> `enemy -list-servers -mode both`.

### C) Refresh listy w trakcie pracy

```
refresh
```

Ponownie strzela do `bot.ircnet.info`, podmienia listę. Już
istniejące klony zostają na swoich serwerach; przyszłe `load`-y
będą rozdzielane po nowej liście.

---

## Komendy shella `enemy>`

```
help                       skrót komend
stat                       lista klonów: id, status, family, bind, nick, server
ipmode <ipv4|ipv6|both>    przełącza tryb DLA NOWYCH klonów (istniejące zostają)
servers [N]                pokazuje N pierwszych serwerów z cache (default 30)
refresh                    refetch listy z ircnet.info
pool                       wypisuje wykryte lokalne IP (v4 + v6)
load N                     spawnuje N nowych klonów
join <#kanał>              wszystkie klony dołączają (też przyszłe)
part <#kanał>              wszystkie klony part-ują
msg <target> <text>        każdy online klon wysyła PRIVMSG
notice <target> <text>     j.w. ale NOTICE
say <#kanał> <text>        jeden losowy klon mówi (mniej spamu)
raw <linia>                jeden losowy klon wysyła surową linię IRC
mode <target> <flagi>      jeden losowy klon ustawia tryb
kick <#kanał> <nick> [r]   jeden losowy klon próbuje kicka, [r] = nadpisanie reasona
reasons                    lista załadowanych kick-reasonów
op   [#kanał] <nick...>    wszyscy online próbują MODE +o (post-takeover op grant)
deop [#kanał] <nick...>    wszyscy online próbują MODE -o
del <id>                   killuje konkretnego klona po ID
disco                      QUIT wszystkich, shell zostaje
exit / quit / q            QUIT wszystkich + wyjście
```

Argumenty z spacjami można obejmować w `"..."`:

```
say #polska "siema chłopaki, no co tam"
```

Zarówno `cmd ...` jak i `.cmd ...` / `/cmd ...` działa (prefix `.` lub `/` jest zjadany).

---

## Takeover + `.op` — typowy flow

1. Spawnujesz klony i dołączasz do pustego (lub ulewanego) kanału:
   ```
   load 5
   join #target
   ```
2. Pierwsze klony wchodzące na pusty kanał IRCnet auto-opują same siebie
   (klasyczne `+o` od serwera dla pierwszego usera).
3. Z poziomu klonów-opów rozdajesz opa swojemu botowi/userowi:
   ```
   op #target myBot
   op #target friendNick anotherFriend yetAnother
   ```
   Komenda blastuje `MODE #target +o ...` ze WSZYSTKICH online-klonów.
   Klony bez opów dostaną od serwera 482 (`Channel operator privileges
   needed`) i to się skończy noopem; opowane klony przepuszczą — efekt:
   wskazany nick dostaje `+o`. Batchowanie jest po 3 nicki na komendę
   (limit IRCnet 2.11).
4. Pełen takeover-set: `kick`, potem `op` swojego bota:
   ```
   kick #target oldOp
   op #target myBot
   ```
   `kick` bez podanego reasona losuje z `kick-reasons` (patrz niżej).

`deop` działa identycznie, tylko `-o`.

Jeśli na kanale jest tylko jeden auto-joinowany kanał, możesz
pominąć `#kanał`:

```
join #target
op myBot
```

---

## Kick-reasony

Aktualnie zaszyte (po podaniu `reasons` w shellu):

```
1.  End of transmission.
2.  Channel sanitized.
3.  Connection refused by ownership.
4.  Welcome to /dev/null.
5.  Compiled with intent, deployed without remorse.
6.  You logged into the wrong network.
7.  Recompiled, redeployed, removed.
8.  Better luck on a different server.
9.  goodbye and thanks for all the fish.
10. connection terminated by upstream policy.
11. manual override engaged.
12. buffer overflow detected, flushing.
13. out of bounds — see you later.
14. channel cleanup in progress.
15. return to sender.
16. this incident has been logged.
```

Suffix `[PT]` (Pojeby Team) został usunięty — lista zawiera czysto
neutralne komunikaty.

Nadpisanie z pliku — jedna linia = jeden reason, `#` na początku linii
to komentarz:

```
enemy -mode ipv6 -n 5 -kick-reasons /home/y/my.txt
```

Pojedynczy kick z własnym reasonem:

```
kick #target nick custom reason here
```

---

## Quit-reasony

Generic disconnect strings (Ping timeout, Read error: Connection reset
by peer, EOF from client itd.) — żeby klony wyglądały jak normalny user
wypadający z sieci. Zmiana z pliku:

```
enemy -mode ipv6 -n 5 -reasons /home/y/quits.txt
```

---

## Diagnostyka

* Klon nie łączy się: zazwyczaj rate-limit konkretnego serwera albo
  ban /64 boxa. `irc.atw-inter.net` w szczególności rzuca
  `Too many host connections (local)` dla naszego /64 — to nie błąd
  skryptu, server-side limit. Spróbuj `-servers 'inny.host'` albo
  `mode=ipv4` jeśli problem jest tylko po v6.
* IRCnet `2a01:6ee0:b:b801::/64` jest banowany jako *Drones* (memory
  projektu news-connect). Klony z tego /64 będą rzucane przez wiele
  serwerów. Praktycznie działa `2a01:6ee0:b:b811::1` i adresy z
  `2a01:6ee0:b:b8ff::/64` — pula `pool` w shellu pokaże, co masz
  pod ręką.
* Backoff przy reconnect: 5s → 10s → … → max 90s.

---

## Build z źródeł

Na boxie nie ma Go. Lokalnie u ciebie:

```bash
cd ~/enemy-go-src   # po rozpakowaniu enemy-go-src.tgz
go build -o enemy ./...
```

Wymaga `go >= 1.23.2` i internetu (pobiera `kofany/go-ircevo`,
`golang.org/x/net`, `golang.org/x/text`, `h12.io/socks`).

Po builcie:

```bash
sudo install -m 0755 ./enemy /usr/local/bin/enemy
```
