package chat

import (
	"regexp"
	"strings"

	"github.com/olegmatyakubov/go-assistant/internal/domain/valueobject"
)

type RuleClassifier struct {
	rules []classifierRule
}

type classifierRule struct {
	pattern    *regexp.Regexp
	route      valueobject.Route
	tools      []string
	confidence float64
}

func NewRuleClassifier() *RuleClassifier {
	c := &RuleClassifier{}

	c.addRule(`(?i)(статус|status|баланс|balance|позици|position|pnl|p&l|что с ботом)`, valueobject.RouteTrading, []string{"trading_status"}, 0.95)
	c.addRule(`(?i)(загугли|google|найти в интернете|look up|в интернете)`, valueobject.RouteSearch, []string{"search_web"}, 0.95)
	c.addRule(`(?i)(напиши код|write code|поправь код|fix code|баг|bug|рефактор|refactor|implement|реализуй)`, valueobject.RouteCode, nil, 0.95)
	c.addRule(`(?i)(nginx|сервер|server|конфиг|config|деплой|deploy|перезапусти|restart|systemctl)`, valueobject.RouteTool, []string{"bash"}, 0.95)
	c.addRule(`(?i)(облак|cloud|mail\.ru|объект|обьект|документ|выписк|егрн|скачай|download|прочитай|смета|акт КС|договор подряд|кс-2|кс-3|разрешен\w+ на строит|мебель|склад\b|магазин|гараж|участок|строительств|проанализируй|анализ|подпис|\.sig\b|сертификат|чертеж|чертёж|pdf|техплан|техническ\w+ план|кадастр)`, valueobject.RouteTool, []string{"drive_files", "projects", "cloud_files", "read_pdf", "inspect_signature", "bash"}, 0.95)
	// Norm citations. A question naming a law, a пункт or a СП must reach the
	// corpus, otherwise the model answers from memory and invents numbers.
	c.addRule(`(?i)(нормати|нормы|норма\b|(^|[^а-яё])сп\s*\d|снип|гост|санпин|стать[яеи]|(^|[^а-яё])ст\.?\s*\d|пункт|(^|[^а-яё])п\.\s*\d|218-фз|грк|кодекс|постановлен[а-яё]*\s+правительства|(^|[^а-яё0-9])пп\s*87)`, valueobject.RouteTool, []string{"norm_search"}, 0.95)
	// Project bookkeeping: route, stage, registry, commercial proposal. Without
	// this the model has no way to learn a route and starts inventing stages.
	c.addRule(`(?i)(проект\w*|этап\w*|маршрут\w*|реестр\w*|\bкп\b|коммерческ\w+ предложен|дорожн\w+ карт)`, valueobject.RouteTool, []string{"projects", "drive_files"}, 0.95)
	// Mail. The courier files attachments into Drive on its own, so questions
	// about letters usually end in a folder — hence drive_files alongside gmail.
	c.addRule(`(?i)(почт\w*|письм\w*|gmail|входящ\w*|вложени\w*|отправител\w*|переписк\w*|тендер\w*|черновик\w*|адресат\w*|запрос\w* (кп|предложен))`, valueobject.RouteTool, []string{"gmail", "contractors", "drive_files", "projects"}, 0.95)
	// Contractors and mailings by trade group. The group is the unit of a
	// tender: "разошли ТЗ по геодезии" has to resolve to addressees.
	c.addRule(`(?i)(подрядчик\w*|контрагент\w*|исполнител\w*|разошл\w*|рассыл\w*|разослать|групп\w* (подрядчиков|контрагентов)|геодези\w*|геологи\w*|изыскани\w*)`, valueobject.RouteTool, []string{"contractors", "gmail"}, 0.95)
	// Explicit Drive mentions, so storage questions do not depend on subject words.
	// Instances without Google simply have no drive_files, and the registry skips it.
	c.addRule(`(?i)(гугл ?диск|google ?drive|драйв|на диске|в диске)`, valueobject.RouteTool, []string{"drive_files"}, 0.95)
	// Moving an object's documents from the Mail.ru archive into its Drive
	// folder: both storages have to be on the table at once.
	c.addRule(`(?i)(перенес\w*|перенос\w*|перелож\w*|скопир\w*|мигрир\w*|импорт\w*)`, valueobject.RouteTool, []string{"drive_files", "cloud_files", "projects"}, 0.95)
	// Drawing and photo editing. Instances without an llm.image block have no
	// generate_image tool, and the registry drops the name.
	c.addRule(`(?i)(нарисуй|рисуй|нарисов|сгенерируй картинк|сгенерируй изображен|сделай картинк|сделай мем|картинк\w*|изображени\w*|фотк\w*|фотограф\w*|на фото|отредактируй|отфотошоп|фотошоп|draw|generate an? image|edit (the )?(photo|image|picture)|paint)`, valueobject.RouteTool, []string{"generate_image"}, 0.9)
	c.addRule(`(?i)(привет|hello|здравствуй|добрый день|добрый вечер|доброе утро|good morning|good evening)`, valueobject.RouteChat, nil, 0.9)

	return c
}

func (c *RuleClassifier) addRule(pattern string, route valueobject.Route, tools []string, confidence float64) {
	c.rules = append(c.rules, classifierRule{
		pattern:    regexp.MustCompile(pattern),
		route:      route,
		tools:      tools,
		confidence: confidence,
	})
}

// stickyDepth — сколько предыдущих реплик учитывать при подборе инструментов.
const stickyDepth = 6

// stickyLimit — сколько инструментов доносить из контекста. Каждая схема стоит
// токенов, поэтому берём только несколько последних по свежести.
const stickyLimit = 4

// ClassifyWithContext добирает инструменты из недавних реплик диалога.
//
// Реплика-продолжение почти никогда не содержит ключевых слов: «переделай на
// эту ссылку», «черновики не обновились», «вместо названия пиши Коллеги». По
// одному последнему сообщению классификатор терял почту и подрядчиков, и бот
// честно отвечал «инструмент почты недоступен» посреди работы над рассылкой.
// Маршрут и уверенность берутся только у текущего сообщения — контекст лишь
// оставляет инструменты под рукой.
func (c *RuleClassifier) ClassifyWithContext(text string, history []string) (valueobject.Route, []string, float64) {
	route, tools, confidence := c.Classify(text)

	seen := make(map[string]bool, len(tools))
	for _, t := range tools {
		seen[t] = true
	}

	added := 0
	for i := len(history) - 1; i >= 0 && len(history)-i <= stickyDepth && added < stickyLimit; i-- {
		_, prev, _ := c.Classify(history[i])
		for _, t := range prev {
			if seen[t] || added >= stickyLimit {
				continue
			}
			seen[t] = true
			tools = append(tools, t)
			added++
		}
	}
	return route, tools, confidence
}

func (c *RuleClassifier) Classify(text string) (valueobject.Route, []string, float64) {
	text = strings.TrimSpace(text)

	// Collect ALL matching tools from all rules
	var allTools []string
	var bestRoute valueobject.Route
	var bestConfidence float64
	seen := make(map[string]bool)

	for _, rule := range c.rules {
		if rule.pattern.MatchString(text) {
			if rule.confidence > bestConfidence {
				bestConfidence = rule.confidence
				bestRoute = rule.route
			}
			for _, t := range rule.tools {
				if !seen[t] {
					seen[t] = true
					allTools = append(allTools, t)
				}
			}
		}
	}

	if len(allTools) > 0 {
		return bestRoute, allTools, bestConfidence
	}

	return valueobject.RouteChat, nil, 0.5
}
