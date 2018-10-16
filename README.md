# @ValidatorInfoBot
Инструментальный бот мессенджера Telegram для управления мастернодой валидатора, блокчейн-сети Minter.

## Зависимость от другого ПО
Используется база данных MongoDB

## Сборка из исходников
```bash
go get github.com/go-telegram-bot-api/telegram-bot-api gopkg.in/ini.v1 gopkg.in/mgo.v2 gopkg.in/mgo.v2/bson github.com/MinterTeam/minter-go-node/core/transaction github.com/MinterTeam/minter-go-node/core/types github.com/MinterTeam/minter-go-node/crypto github.com/MinterTeam/minter-go-node/rlp
go build -o tbotd telegram_bot.go
```

## Настройка
В файле cmc0.ini укажите IP адрес мастерноды Minter, IP адрес сервера базы данных MongoDB и TelegramAPI-токен.

## Установка для Ubuntu
Поместите файлы tbotd и cmc0.ini в каталог /opt/tbot/.

Скопируйте файл other/tbot.service в каталог /etc/systemd/system/ и выполните команды:

```bash
sudo systemctl enable tbot
sudo systemctl start tbot
```

## Команды в боте
* __/node_info__ - информация о мастерноде привязанной к пользователю
* __/node_info__ *[часть-pubkey]* - поиск мастернод валидаторов по части публичного ключа и выдача информации по ним
* __/node_add__ *[pubkey]* - добавление мастерноды для мониторинга за ней и привязка её к пользователю
* __/node_edit__ *[pubkey]* - изменение публичного ключа наблюдаемой мастерноды, которая привязанна к пользователю
* __/node_del__ - удаление мастерноды из мониторинга и очитска данных
* __/candidate__ *[on/off/1/0]* - включить или отключить мастерноду (!-только если привязан PrivKey)
* __/notification__ - вкл/откл уведомление об исключение мастерноды из списка валидаторов
* __/start__ и __/help__ - отобразя помощь по командам

## TODO:
- [ ] База данных MySQL, Redis
- [ ] Мультиязычность

### Лицензия MIT
