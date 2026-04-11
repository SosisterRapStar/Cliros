Аннотация

Выпускная квалификационная работа посвящена разработке библиотеки на языке Go, предоставляющей интерфейс для построения сервисов с применением паттерна Saga. Актуальность работы обусловлена широким распространением микросервисной архитектуры, необходимостью надежной координации сквозных бизнес-процессов в условиях распределенного хранения данных и востребованностью Go при разработке микросервисных систем.

В работе рассмотрены подходы к реализации распределенных транзакций, проанализирован паттерн Saga и существующие решения в данной области. Спроектирована и реализована библиотека для описания шагов распределенного процесса и скрывающая типовые инфраструктурные задачи, связанные с атомарностью публикации сообщений, надежностью выполнения, расширяемостью и наблюдаемостью. Для демонстрации применения библиотеки разработана тестовая система, моделирующая распределенный сценарий бронирования путешествия.

Abstract

This graduation thesis is devoted to the development of a Go library that provides abstractions for building distributed systems based on the Saga pattern. The relevance of the work is determined by the widespread adoption of microservice architecture and the need for reliable coordination of end-to-end business processes in distributed data environments.

The thesis examines approaches to distributed transaction implementation, analyzes the Saga pattern, and reviews existing solutions in this area. A library is designed and implemented that provides interfaces for describing the steps of a distributed process while hiding typical infrastructure concerns related to atomic message publishing, execution reliability, extensibility, and observability. To demonstrate the use of the library, a test system modeling a distributed travel booking scenario is developed. Unit and end-to-end testing are performed, including both the successful execution path and the compensation scenario.