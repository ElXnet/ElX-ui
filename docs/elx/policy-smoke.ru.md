# Сквозная проверка применения политики ElX

## Один запуск из WSL

В Linux или WSL установите Go версии из `go.mod`, C-компилятор для `-race`, OpenSSL и Python 3. Если своей тестовой БД нет, запустите Docker Desktop с интеграцией WSL. В каталоге для тестов выполните:

```bash
git clone --branch restrict-runtime-unlimited https://github.com/ElXnet/ElXray.git
git clone --branch elx-control-journal https://github.com/ElXnet/ElX-ui.git
cd ElX-ui
set -o pipefail
bash scripts/elx-test-local.sh 2>&1 | tee elx-test.log
```

Если репозитории уже скачаны, обновите указанные ветки через `git pull --ff-only`. Сценарий печатает коммиты, собирает оба бинарника, запускает Go-тесты с PostgreSQL и `-race`, проверяет доставку на две ноды и отключение открытого VLESS/TCP-соединения. На успехе выводит `PASS: ElX local suite`. При сбое приложите `elx-test.log`.

По умолчанию сценарий поднимает временный контейнер PostgreSQL и удаляет его после прогона. Для существующей **тестовой** базы задайте `ELX_CONTROL_POSTGRES_DSN`. Для ElXray из другого каталога задайте `ELXRAY_SOURCE`. Сквозная проверка VLESS использует локальный echo-сервер. Она проверяет отключение подписки целиком. Проверки идентификации устройства в реальном VPN-потоке и передачи слота между нодами добавляются вместе с этим контуром.

## Ручной запуск проверки доставки

Стенд запускает два экземпляра ElXray с разными ключами и одной логической подпиской. Control доставляет политики каждому экземпляру независимо. Команда проходит через PostgreSQL, gRPC с mTLS, ElXray и подписанное подтверждение. Команды нода забирает по одной через `apply-policy`. Ревизия ядра 1 соответствует ревизии журнала 0. Идентификатор операции сохраняется при повторной доставке.

Запускайте команды из Linux или WSL. Достаточно одного компьютера. Go и OpenSSL должны быть доступны внутри WSL, Docker Desktop должен быть подключён к этой WSL-дистрибуции. Скрипт применяет миграцию и оставляет записи в отдельной тестовой базе.

1. Возьмите ветку `elx-control-journal` в ElX-ui и `restrict-runtime-unlimited` в ElXray. В примере репозитории лежат рядом.
2. Соберите бинарники. Версию Go берите из `go.mod` ElX-ui.

   ```bash
   cd ElXray
   go build -o ../elxray-lab ./main
   cd ../ElX-ui
   go build -o ../elx-control-lab ./cmd/elx-control
   ```

3. Запустите отдельную тестовую базу и дождитесь готовности PostgreSQL.

   ```bash
   docker run -d --name elx-test-pg -p 127.0.0.1:55432:5432 \
     -e POSTGRES_USER=elx -e POSTGRES_PASSWORD=elx_test \
     -e POSTGRES_DB=elx_test postgres:18
   docker exec elx-test-pg pg_isready -U elx -d elx_test
   ```

4. Когда `pg_isready` покажет `accepting connections`, запустите тест.

   ```bash
   export ELX_CONTROL_POSTGRES_DSN='host=127.0.0.1 port=55432 user=elx dbname=elx_test password=elx_test sslmode=disable'
   export ELXRAY_BIN="$(realpath ../elxray-lab)"
   export ELX_CONTROL_BIN="$(realpath ../elx-control-lab)"
   bash scripts/elx-policy-smoke.sh
   ```

5. Ожидаемый вывод: `PASS: mTLS, two nodes, wrong-core rejection, durable restart and replay`. При сбое пришлите вывод скрипта и версии бинарников. Не присылайте ключи, сертификатные seed или DSN с паролем. Если порты заняты, задайте `ELX_SMOKE_CONTROL_PORT` `ELX_SMOKE_CORE_PORT` и `ELX_SMOKE_SECOND_PORT`. После проверки тестовый контейнер можно остановить командой `docker stop elx-test-pg`.

Скрипт создаёт временный каталог с закрытыми правами. В нём остаются конфигурация, ключи стенда и журналы. Каталог можно удалить после проверки.

В постоянном окружении настройте отдельный CA нод и выдайте каждой ноде клиентский сертификат с URI SAN `spiffe://elxnet/node/<uuid>`. Control читает список привязок из JSON-файла `ELX_POLICY_BINDING`. На одной ноде локальная подписка соответствует одному ресурсу журнала. Ноды используют разные публичные ключи ElXray. При нескольких привязках `enqueue-policy` требует `-node` и `-resource`. Приватный seed и манифест состояния принадлежат процессу ядра. Хранилище располагается на локальной файловой системе, а процесс запускается с `ELXRAY_POLICY_STATE`. Стенд использует TCP loopback. Для локального агента на хосте предусмотрен `ELXRAY_POLICY_SOCKET` с ограничением доступа правами ОС.

Подпись подтверждает сохранённую конфигурацию конкретной подписки и привязана к ноде, ресурсу, операции и эпохе. Она не подтверждает закрытие потоков, безопасное перераспределение бюджета или защиту от восстановления устаревшей копии диска. Для этих сценариев потребуются отдельные контракты и проверки. При смене привязки либо восстановлении снимка управляющий контур следует остановить до сверки состояния.
