package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-yaml/yaml"
	"github.com/yhat/scrape"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

type Config struct {
	Account []struct {
		Name   string `yaml:"name"`
		Cookie string `yaml:"cookie"`
	} `yaml:"account"`
	Push struct {
		BotToken string `yaml:"bot_token"`
		ChatID   string `yaml:"chat_id"`
	} `yaml:"push"`
}

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		MaxIdleConnsPerHost: 10, // 每个 Host 最大空闲连接数
	},
}

func loadConfig(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

func sendRequest(method, url string, body string, headers map[string]string, cookie string) ([]byte, error) {
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	return data, nil
}

func tsdmCheckIn(cookie string) (string, error) {
	// 获取formhash
	data, err := sendRequest("GET", "https://www.tsdm39.com/forum.php", "", nil, cookie)
	if err != nil {
		return "", fmt.Errorf("获取 formhash 失败: %w", err)
	}

	formhashRegex := regexp.MustCompile(`formhash=(.+?)"`)
	formhash := formhashRegex.FindStringSubmatch(string(data))[1]

	// 签到
	formData := url.Values{
		"formhash":  {formhash},
		"qdxq":      {"kx"},
		"qdmode":    {"3"},
		"todaysay":  {""},
		"fastreply": {"1"},
	}

	headers := map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"Origin":       "https://www.tsdm39.com",
	}

	respData, err := sendRequest("POST", "https://www.tsdm39.com/plugin.php?id=dsu_paulsign%3Asign&operation=qiandao&infloat=1&sign_as=1&inajax=1", formData.Encode(), headers, cookie)
	if err != nil {
		return "", fmt.Errorf("签到请求失败: %w", err)
	}

	// 检查签到结果
	successRegex := regexp.MustCompile(`签到成功`)
	alreadyRegex := regexp.MustCompile(`您今日已经签到`)

	// 使用 html.Parse 解析 HTML 代码
	reader, err := charset.NewReader(strings.NewReader(string(respData)), http.DetectContentType(respData))
	if err != nil {
		return "", fmt.Errorf("创建 reader 失败: %w", err)
	}

	root, err := html.Parse(reader)
	if err != nil {
		return "", fmt.Errorf("解析 HTML 失败: %w", err)
	}

	// 查找包含签到结果的 div 元素
	divMatcher := scrape.ByClass("c")
	div, ok := scrape.Find(root, divMatcher)
	if !ok {
		return "", fmt.Errorf("找不到包含签到结果的 div 元素")
	}

	// 获取签到结果文本
	resultText := strings.TrimSpace(scrape.Text(div))

	if successRegex.MatchString(resultText) {
		return "签到成功", nil
	} else if alreadyRegex.MatchString(resultText) {
		return "您今天已经签到", nil
	} else {
		return "", fmt.Errorf("签到失败: %s", resultText)
	}
}

func tsdmWork(accountName, cookie string) (bool, time.Duration, error) {
	headers := map[string]string{
		"User-Agent":       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		"Connection":       "Keep-Alive",
		"X-Requested-With": "XMLHttpRequest",
		"Referer":          "https://www.tsdm39.net/plugin.php?id=np_cliworkdz:work",
		"Content-Type":     "application/x-www-form-urlencoded",
	}

	// 检查是否可以打工
	data, err := sendRequest("GET", "https://www.tsdm39.com/plugin.php?id=np_cliworkdz%3Awork&inajax=1", "", headers, cookie)
	if err != nil {
		return false, 0, fmt.Errorf("检查打工状态失败: %w", err)
	}

	waitRegex := regexp.MustCompile(`您需要等待(\d+)小时(\d+)分钟(\d+)秒后即可进行。`)
	if waitRegex.MatchString(string(data)) {
		matches := waitRegex.FindStringSubmatch(string(data))
		hours, _ := strconv.Atoi(matches[1])
		minutes, _ := strconv.Atoi(matches[2])
		seconds, _ := strconv.Atoi(matches[3])
		waitDuration := time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second
		return false, waitDuration, nil // 返回计算出的等待时间
	}

	// 打工
	formData := url.Values{"act": {"clickad"}}
	for i := 0; i < 6; i++ {
		_, err := sendRequest("POST", "https://www.tsdm39.com/plugin.php?id=np_cliworkdz:work", formData.Encode(), headers, cookie)
		if err != nil {
			fmt.Printf("[%s] 打工请求失败: %v\n", accountName, err)
			return false, 0, fmt.Errorf("打工请求失败: %w", err)
		}

		time.Sleep(3 * time.Second)
	}

	// 获取奖励
	formData = url.Values{"act": {"getcre"}}
	data, err = sendRequest("POST", "https://www.tsdm39.com/plugin.php?id=np_cliworkdz:work", formData.Encode(), headers, cookie)
	if err != nil {
		fmt.Printf("[%s] 获取奖励失败: %v\n", accountName, err)
		return false, 0, fmt.Errorf("获取奖励失败: %w", err)
	}

	// 检查是否打工成功
	successRegex := regexp.MustCompile(`恭喜，您已经成功领取了奖励天使币 \+\d+`)
	if successRegex.MatchString(string(data)) {
		fmt.Printf("[%s] 打工成功\n", accountName)
		return true, 6 * time.Hour, nil // 打工成功后，返回 6 小时的等待时间
	}

	fmt.Printf("[%s] 打工失败: %s\n", accountName, string(data))
	return false, 0, fmt.Errorf("打工失败: %s", string(data))
}

func getScore(cookie string) (string, error) {
	data, err := sendRequest("GET", "https://www.tsdm39.com/home.php?mod=spacecp&ac=credit&showcredit=1", "", nil, cookie)
	if err != nil {
		return "", fmt.Errorf("获取积分信息失败: %w", err)
	}

	reader, err := charset.NewReader(strings.NewReader(string(data)), http.DetectContentType(data))
	if err != nil {
		return "", fmt.Errorf("创建 reader 失败: %w", err)
	}

	root, err := html.Parse(reader)
	if err != nil {
		return "", fmt.Errorf("解析 HTML 失败: %w", err)
	}

	ulMatcher := scrape.ByClass("creditl")
	ul, ok := scrape.Find(root, ulMatcher)
	if !ok {
		return "", fmt.Errorf("找不到 ul 元素")
	}

	liMatcher := scrape.ByClass("xi1")
	li, ok := scrape.Find(ul, liMatcher)
	if !ok {
		return "", fmt.Errorf("找不到 li 元素")
	}

	angelCoins := strings.TrimSpace(strings.Replace(scrape.Text(li), "天使币:", "", 1))
	return angelCoins, nil
}

func telegramPush(sendTitle, pushMessage, botToken, chatID string) error {
	formData := url.Values{
		"chat_id": {chatID},
		"text":    {sendTitle + "\r\n" + pushMessage},
	}

	headers := map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	}

	_, err := sendRequest("POST", "https://api.telegram.org/bot"+botToken+"/sendMessage", formData.Encode(), headers, "")
	if err != nil {
		return fmt.Errorf("telegram 推送失败: %w", err)
	}
	return nil
}

func push(data, botToken, chatID string) {
	err := telegramPush("【天使动漫论坛任务推送】", data, botToken, chatID)
	if err != nil {
		fmt.Println(err)
	}
}

func pushCheckInResult(accountName, result, botToken, chatID string) {
	if strings.Contains(result, "签到成功") {
		push(fmt.Sprintf("[%s] 签到结果: %s", accountName, result), botToken, chatID)
	}
}

func pushWorkResult(accountName string, success bool, score, botToken, chatID string) {
	if success {
		push(fmt.Sprintf("[%s] 打工成功，已拥有天使币数量: %s", accountName, score), botToken, chatID)
	}
}

func runCheckIn(accountName, cookie, botToken, chatID string) {
	checkInResult, err := tsdmCheckIn(cookie)
	if err != nil {
		fmt.Printf("[%s] 签到错误: %v\n", accountName, err)
	} else {
		fmt.Printf("[%s] %s\n", accountName, checkInResult)
		pushCheckInResult(accountName, checkInResult, botToken, chatID)
	}
}

func runWork(accountName, cookie, botToken, chatID string) time.Duration {
	workSuccess, waitDuration, err := tsdmWork(accountName, cookie)
	if err != nil {
		fmt.Printf("[%s] 打工错误: %v\n", accountName, err)
		push(fmt.Sprintf("[%s] 打工失败: %v", accountName, err), botToken, chatID) // 推送打工失败信息
	} else {
		score, scoreErr := getScore(cookie)
		if scoreErr != nil {
			fmt.Printf("[%s] 获取天使币数量失败: %v\n", accountName, scoreErr)
		} else {
			fmt.Printf("[%s] 天使币数量: %s\n", accountName, score)
			if workSuccess { // 只在打工成功时推送打工成功信息
				pushWorkResult(accountName, workSuccess, score, botToken, chatID)
			}
		}

		if !workSuccess && waitDuration == 0 {
			waitDuration = 1 * time.Minute
		}

		fmt.Printf("[%s] 下次打工将在 %s 后进行\n", accountName, waitDuration)
	}
	return waitDuration
}

func run(config *Config, daemonMode bool) {
	if daemonMode {
		if os.Getppid() != 1 {
			pid, err := syscall.ForkExec(os.Args[0], os.Args, &syscall.ProcAttr{
				Files: []uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()},
			})
			if err != nil {
				fmt.Println("无法创建子进程:", err)
				return
			}
			fmt.Printf("后台进程已启动，PID: %d\n", pid)
			os.Exit(0)
		}

		// 将标准输出和标准错误重定向到文件
		logFile, err := os.OpenFile("tsdm.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			fmt.Println("无法打开日志文件:", err)
			return
		}
		defer logFile.Close()

		os.Stdout = logFile
		os.Stderr = logFile

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

		var wg sync.WaitGroup
		wg.Add(len(config.Account) * 2) // 为每个账户的签到和打工任务添加计数

		for _, account := range config.Account {
			// 后台模式下，每天凌晨 0 点执行一次签到
			go func(acc struct {
				Name   string `yaml:"name"`
				Cookie string `yaml:"cookie"`
			}) {
				defer wg.Done() // 确保签到任务完成后计数器减一
				for {
					now := time.Now()
					nextMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Add(24 * time.Hour)
					time.Sleep(nextMidnight.Sub(now)) // 等待到第二天凌晨
					runCheckIn(acc.Name, acc.Cookie, config.Push.BotToken, config.Push.ChatID)
				}
			}(account)

			// 打工逻辑保持不变
			go func(acc struct {
				Name   string `yaml:"name"`
				Cookie string `yaml:"cookie"`
			}) {
				defer wg.Done() // 确保打工任务完成后计数器减一
				for {
					waitDuration := runWork(acc.Name, acc.Cookie, config.Push.BotToken, config.Push.ChatID)
					time.Sleep(waitDuration)
				}
			}(account)
		}

		wg.Wait() // 等待所有签到和打工任务完成

	} else {
		for _, account := range config.Account {
			runCheckIn(account.Name, account.Cookie, config.Push.BotToken, config.Push.ChatID)
			runWork(account.Name, account.Cookie, config.Push.BotToken, config.Push.ChatID)
		}
	}
}

func main() {
	configPath := flag.String("c", "config.yaml", "配置文件路径")
	daemonMode := flag.Bool("d", false, "是否以后台守护进程方式运行")
	flag.Parse()

	config, err := loadConfig(*configPath)
	if err != nil {
		fmt.Println("加载配置文件失败:", err)
		return
	}

	run(config, *daemonMode)
}
