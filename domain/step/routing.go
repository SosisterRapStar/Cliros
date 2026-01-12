package step

type RoutingConfig struct {
	NextStepTopics []string
	// здесь можно указать разные топики
	// например, если человек не хочет отправлять компенсацию по цепочке,
	// а хочет отправить ее нескольким сервисам
	ErrorTopics []string
}
