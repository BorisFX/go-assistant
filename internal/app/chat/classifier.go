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
	c.addRule(`(?i)(облак|cloud|mail\.ru|объект|обьект|документ|выписк|егрн|скачай|download|прочитай|смета|акт КС|договор подряд|кс-2|кс-3|разрешен\w+ на строит|мебель|склад\b|магазин|гараж|участок|строительств|проанализируй|анализ|подпис|\.sig\b|сертификат|чертеж|чертёж|pdf|техплан|техническ\w+ план|кадастр)`, valueobject.RouteTool, []string{"cloud_files", "read_pdf", "inspect_signature", "bash"}, 0.95)
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
