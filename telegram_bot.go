/*
MIT License

Copyright (c) 2018 Validator.Center

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api"
	"gopkg.in/ini.v1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	// для транзакций от Минтер
	tr "github.com/MinterTeam/minter-go-node/core/transaction"
	"github.com/MinterTeam/minter-go-node/core/types"
	"github.com/MinterTeam/minter-go-node/crypto"
	"github.com/MinterTeam/minter-go-node/rlp"

	// для транзакций старндартные
	"bytes"
	"encoding/hex"
)

// пока данные будем хранить в памяти
var (
	CoinMinter      string // Основная монета Minter
	allValid        []candidate_info
	allUser         []usrData
	MnAddress       string // MasterNode
	MnAddressReserv string // резервный
	TgTokenAPI      string // Токен к API телеграма
	TgTimeUpdate    int64  // Время в сек. обновления статуса
	DBAddress       string // MongoDB
	HelpMsg         = "Это простой мониторинг доступности мастерноды валидатора и краткая информация о ней.\n" +
		"Список доступных комманд:\n" +
		"/node_info - информация о мастерноде привязанной к пользователю\n" +
		"/node_info [часть-pubkey] - информация о мастернодах найденных по части указанного ключа\n" +
		"/node_add [pubkey] - добавление мастерноды для мониторинга состояния и привязка её к пользователю\n" +
		"/node_edit [pubkey] - изменение мастерноды для мониторинга привязанной к пользователю\n" +
		"/node_del - удаление мастерноды из мониторинга и очитска данных\n" +
		"/candidate [on/off/1/0] - включить или отключить мастерноду (!-только если привязан PrivKey)\n" +
		"/notification - вкл/откл уведомление об исключение мастерноды из списка валидаторов\n" +
		"/start - отобразить это сообщение\n" +
		"/help - отобразить это сообщение\n\n" +
		"Начните с привязки мастерноды для мониторинга!"
)

// Структура данных пользователя
type usrData struct {
	ChatID       int64  `bson:"chat_id"`
	UserName     string `bson:"user_name"`
	UserAddress  string `bson:"user_address"`
	PubKey       string `bson:"pub_key"`
	PrivKey      string `bson:"priv_key"`
	Notification bool   `bson:"notification"`
}

// Ответ от запроса о количестве транзакций
type count_transaction struct {
	Code   int                `json:"code"`
	Result TransCountResponse `json:"result"`
}
type TransCountResponse struct {
	Count int `json:"count"`
}

// Ответ транзакции
type send_transaction struct {
	Code   int               `json:"code"`
	Result TransSendResponse `json:"result"`
	Log    string            `json:"log"`
}
type TransSendResponse struct {
	Hash string `json:"hash"`
}

// запрос по валидаторам
type node_validators struct {
	Code   int
	Result []result_valid
}

// результат по валидаторам
type result_valid struct {
	AccumulatedReward   string `json:"accumulated_reward"`
	AccumulatedReward32 float32
	AbsentTimes         int `json:"absent_times"`
	Candidate           candidate_info
}

// структура кандидата/валидатора
type candidate_info struct {
	CandidateAddress string        `json:"candidate_address" bson:"candidate_address"`
	TotalStake       string        `json:"total_stake" bson:"total_stake"`
	TotalStake32     float32       `bson:"total_stake32"`
	PubKey           string        `json:"pub_key" bson:"pub_key"`
	Commission       int           `json:"commission" bson:"commission"`
	CreatedAtBlock   int           `json:"created_at_block" bson:"created_at_block"`
	StatusInt        int           `json:"status" bson:"status"` // числовое значение статуса: 1 - Offline, 2 - Online
	Stakes           []stakes_info `json:"stakes" bson:"stakes"`
	TimeUpdate       time.Time     `bson:"time_update"` // UPDATE дата последнего обновления
}

// стэк делегатов
type stakes_info struct {
	Owner      string  `json:"owner" bson:"owner"`
	Coin       string  `json:"coin" bson:"coin"`
	Value      string  `json:"value" bson:"value"`
	BipValue   string  `json:"bip_value" bson:"bip_value"`
	Value32    float32 `bson:"value32"`
	BipValue32 float32 `bson:"bip_value32"`
}

// Перевод из строки в число с запятой
func cnvStr2Float_18(amntTokenStr string) float32 {
	var fAmntToken float32 = 0.0
	if amntTokenStr != "" {
		fAmntToken64, err := strconv.ParseFloat(amntTokenStr, 64)
		if err != nil {
			panic(err.Error())
		}
		fAmntToken = float32(fAmntToken64 / 1000000000000000000)
	}
	return fAmntToken
}

// Статус мастерноды
func getNodeStatusString(statInt int) string {
	statStr := "Кандидат"
	if statInt == 2 {
		statStr = "Валидатор"
	}
	return statStr
}

// Сокращение строки
func getMinString(bigStr string) string {
	return fmt.Sprintf("%s...%s", bigStr[:6], bigStr[len(bigStr)-4:len(bigStr)])
}

// Загрузка пользователей из БД в память
func loadAllUsers(session *mgo.Session) {
	// Таблица всех кандидатов candidate_info
	usrCollection := session.DB("mvc_db").C("tabl_bot_usr")
	qUsr := bson.M{}
	usrCollection.Find(qUsr).All(&allUser)
}

// Добавляем нового пользователя в БД и в память
func addUser(session *mgo.Session, usr1 usrData) {
	usrCollection := session.DB("mvc_db").C("tabl_bot_usr")
	err := usrCollection.Insert(usr1)
	if err != nil {
		fmt.Println("ERROR", err)
	}
	//FIXME: но! пока всёравно добавим в память
	allUser = append(allUser, usr1)
}

// Очистка базы (root)
func cleanDB(session *mgo.Session) {
	usrCollection := session.DB("mvc_db").C("tabl_bot_usr")
	_, err := usrCollection.RemoveAll(bson.M{})
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println("очищена - BD")

	// очищаем
	allValid = allValid[:0]
}

// Изменение PubKey и PrivKey мастерноды пользователя в БД и в память
func editUserKey(session *mgo.Session, usr1 usrData) {
	var err error
	usrCollection := session.DB("mvc_db").C("tabl_bot_usr")
	if usr1.PubKey != "" && usr1.PrivKey == "" {
		err = usrCollection.Update(bson.M{"chat_id": usr1.ChatID}, bson.M{"$set": bson.M{"pub_key": usr1.PubKey}})
	} else if usr1.PubKey != "" && usr1.PrivKey != "" {
		err = usrCollection.Update(bson.M{"chat_id": usr1.ChatID}, bson.M{"$set": bson.M{"pub_key": usr1.PubKey, "user_address": usr1.UserAddress, "priv_key": usr1.PrivKey}})
	} else {
		fmt.Println("ERROR", "Что-то пошло не так с изменением _Ключей_")
		return
	}
	if err != nil {
		fmt.Println("ERROR", err)
	}
	//FIXME: но! пока всё-равно добавим в память
	for iU, _ := range allUser {
		if allUser[iU].ChatID == usr1.ChatID {
			allUser[iU].PubKey = usr1.PubKey
			allUser[iU].PrivKey = usr1.PrivKey
			allUser[iU].UserAddress = usr1.UserAddress
		}
	}
}

// Удаление данных о мастерноде
func delNode(session *mgo.Session, chatID int64) {
	var err error
	usrCollection := session.DB("mvc_db").C("tabl_bot_usr")
	err = usrCollection.Update(bson.M{"chat_id": chatID}, bson.M{"$set": bson.M{"pub_key": "", "priv_key": "", "notification": false}})

	if err != nil {
		fmt.Println("ERROR", err)
	}
	//FIXME: но! пока всё-равно добавим в память
	for iU, _ := range allUser {
		if allUser[iU].ChatID == chatID {
			allUser[iU].PubKey = ""
			allUser[iU].PrivKey = ""
			allUser[iU].Notification = false
		}
	}
}

// Изменение PubKey мастерноды пользователя в БД и в память
func editNodeNotif(session *mgo.Session, ChatID int64) string {
	nowStatus := false
	retTxt := ""
	for iU, _ := range allUser {
		if allUser[iU].ChatID == ChatID {
			nowStatus = allUser[iU].Notification
		}
	}
	// Меняем статус
	if nowStatus == true {
		nowStatus = false
		retTxt = "Отключено уведомление об исключение мастерноды из Валидаторов"
	} else {
		nowStatus = true
		retTxt = "Включено уведомление об исключение мастерноды из Валидаторов"
	}

	usrCollection := session.DB("mvc_db").C("tabl_bot_usr")
	err := usrCollection.Update(bson.M{"chat_id": ChatID}, bson.M{"$set": bson.M{"notification": nowStatus}})
	if err != nil {
		fmt.Println("ERROR", err)
	}
	//FIXME: но! пока всёравно добавим в память
	for iU, _ := range allUser {
		if allUser[iU].ChatID == ChatID {
			allUser[iU].Notification = nowStatus
		}
	}
	return retTxt
}

// Возвращает список валидаторов в память
func ReturnValid() {
	url := fmt.Sprintf("%s/api/validators", MnAddress)
	res, err := http.Get(url)
	if err != nil {
		url = fmt.Sprintf("%s/api/validators", MnAddressReserv)
		res, err = http.Get(url)
		if err != nil {
			panic(err.Error())
		}
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		panic(err.Error())
	}

	// очищаем
	allValid = allValid[:0]

	var data node_validators
	json.Unmarshal(body, &data)
	for _, oneNode := range data.Result {
		oneNode.Candidate.TotalStake32 = cnvStr2Float_18(oneNode.Candidate.TotalStake)
		allValid = append(allValid, oneNode.Candidate)
	}
}

// Получаем данные валидатора по его паблик-кею
func getValidInfo(pubKey string) candidate_info {
	var retVld candidate_info
	for _, oneNode := range allValid {
		if oneNode.PubKey == pubKey {
			return oneNode
		}
	}
	return retVld
}

// поиск валидатора по его части паблик-кею или названию
func searchValid(search string) []candidate_info {
	srchUpper := strings.ToUpper(search)
	retVld := []candidate_info{}
	for _, oneNode := range allValid {
		pkUpper := strings.ToUpper(oneNode.PubKey)
		if strings.Contains(pkUpper, srchUpper) == true {
			retVld = append(retVld, oneNode)
		}
	}
	return retVld
}

// Получаем данные по пользователю по его ID чата (или еще по нику?)
func getUser(chatID int64) usrData {
	for _, oneUsr := range allUser {
		if oneUsr.ChatID == chatID {
			return oneUsr
		}
	}
	return usrData{}
}

// Мастернода в списке валидаторов? проверка по паблик-кею
func getStatusValid(pubKey string) bool {
	for _, oneNode := range allValid {
		if oneNode.PubKey == pubKey {
			// Мастернода в списке, но может-быть статус не валидатора
			if oneNode.StatusInt == 2 {
				return true
			} else {
				return false
			}
		}
	}
	return false
}

// Возвращает количество исходящих транзакций с данной учетной записи. Это нужно использовать для расчета nonce для новой транзакции.
func getNonce(txAddress string) int {
	url := fmt.Sprintf("%s/api/transactionCount/%s", MnAddress, txAddress)
	res, err := http.Get(url)
	if err != nil {
		panic(err.Error())
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		panic(err.Error())
	}

	var data count_transaction
	json.Unmarshal(body, &data)

	return data.Result.Count
}

// Функция транзакции вкл/откл мастерноды
func SetCandidateTransaction(usrAddr string, keyString string, pubKeyMN string, status bool) (string, error) {
	privKey, err := crypto.HexToECDSA(keyString)
	if err != nil {
		fmt.Println("ERROR: SetCandidateTransaction::crypto.HexToECDSA")
		return "", err
	}

	var csMNTCoin types.CoinSymbol
	copy(csMNTCoin[:], []byte(CoinMinter))

	trn := tr.Transaction{}
	btPubKey := types.Hex2Bytes(strings.TrimLeft(pubKeyMN, "Mp")) // Убираем префикс и переводим в байты
	if status == true {
		trn.Type = tr.TypeSetCandidateOnline // Включение мастерноды
		trn.Data, _ = rlp.EncodeToBytes(tr.SetCandidateOnData{PubKey: btPubKey})
	} else {
		trn.Type = tr.TypeSetCandidateOffline // Выключение мастерноды
		trn.Data, _ = rlp.EncodeToBytes(tr.SetCandidateOffData{PubKey: btPubKey})
	}
	trn.Nonce = uint64(getNonce(usrAddr) + 1)
	trn.GasCoin = csMNTCoin

	fmt.Printf("INFO:: PK=%s\n TX=%#v\n", pubKeyMN, trn)

	err = trn.Sign(privKey)
	if err != nil {
		fmt.Println("ERROR: SetCandidateTransaction::trn.Sign")
		return "", err
	}

	bts, err := trn.Serialize()
	if err != nil {
		fmt.Println("ERROR: SetCandidateTransaction::trn.Serialize")
		return "", err
	}
	str := hex.EncodeToString(bts)

	message := map[string]interface{}{
		"transaction": str,
	}
	bytesRepresentation, err := json.Marshal(message)
	if err != nil {
		fmt.Println("ERROR: SetCandidateTransaction::json.Marshal")
		return "", err
	}

	url := fmt.Sprintf("%s/api/sendTransaction", MnAddress)
	res, err := http.Post(url, "application/json", bytes.NewBuffer(bytesRepresentation))
	if err != nil {
		fmt.Println("ERROR: SetCandidateTransaction::http.Post")
		return "", err
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		fmt.Println("ERROR: SetCandidateTransaction::ioutil.ReadAll")
		return "", err
	}

	var data send_transaction
	json.Unmarshal(body, &data)

	if data.Code == 0 {
		return data.Result.Hash, nil
	} else {
		fmt.Printf("ERROR: SetCandidateTransaction: %#v\n", data)
		return data.Log, errors.New(fmt.Sprintf("Err:%d", data.Code))
	}
}

// Сам мониторинг! как горутина!
func monitor(bot *tgbotapi.BotAPI) {
	// бесконечный цикл
	for {
		ReturnValid()

		for _, oneUser := range allUser {
			if !getStatusValid(oneUser.PubKey) && oneUser.Notification == true {
				//Алам!
				fmt.Println("NOOOOO! ", oneUser.UserName)
				// отправляем пользователю сообщение
				msg := tgbotapi.NewMessage(oneUser.ChatID, "Нода не в валидаторах!")
				bot.Send(msg)
			}
		}

		fmt.Printf("Пауза %dсек.... в этот момент лучше прерывать\n", TgTimeUpdate)
		time.Sleep(time.Second * time.Duration(TgTimeUpdate)) // пауза
	}
}

func main() {
	ConfFileName := "cmc0.ini"

	// проверяем есть ли входной параметр/аргумент
	if len(os.Args) == 2 {
		ConfFileName = os.Args[1]
	}
	fmt.Printf("INI=%s\n", ConfFileName)

	// INI
	cfg, err := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, ConfFileName)
	if err != nil {
		fmt.Println("Ошибка загрузки INI файла:", err.Error())
		return
	} else {
		fmt.Println("...данные с INI файла = загружены!")
	}
	secMN := cfg.Section("masternode")
	MnAddress = secMN.Key("ADDRESS").String()
	MnAddressReserv = secMN.Key("ADDRESS_2").String()
	secDB := cfg.Section("database")
	DBAddress = secDB.Key("ADDRESS").String()
	netMN := cfg.Section("network")
	CoinMinter = netMN.Key("COINNET").String()
	secTG := cfg.Section("telegram")
	TgTokenAPI = secTG.Key("TOKEN").String()
	_TgTimeUpdate, err := strconv.Atoi(secTG.Key("TIMEUPDATE").String())
	if err != nil {
		fmt.Println(err)
		TgTimeUpdate = 60
	}
	TgTimeUpdate = int64(_TgTimeUpdate)

	// открываем соединение
	session, err := mgo.Dial(DBAddress)
	if err != nil {
		fmt.Println("Ошибка соединения с БД:", err.Error())
		return
	}
	defer session.Close()

	fmt.Println(time.Now())

	// подключаемся к боту с помощью токена
	bot, err := tgbotapi.NewBotAPI(TgTokenAPI)
	if err != nil {
		fmt.Println("Ошибка соединения с Telegram:", err.Error())
		return
	}

	bot.Debug = true
	fmt.Printf("Авторизован: %s\n", bot.Self.UserName)

	// Загружаем пользователей из базы
	loadAllUsers(session)

	// в отдельном потоке запускаем функцию мониторинга
	go monitor(bot)

	// u - структура с конфигом для получения апдейтов
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	// используя конфиг u создаем канал в который будут прилетать новые сообщения
	updates, err := bot.GetUpdatesChan(u)

	// в канал updates прилетают структуры типа Update
	// вычитываем их и обрабатываем
	for update := range updates {
		// универсальный ответ на любое сообщение
		reply := ""
		if update.Message == nil {
			continue
		}
		/*if !update.Message.IsCommand() { // ignore any non-command Messages
			continue
		}*/

		// логируем от кого какое сообщение пришло
		fmt.Printf("[%s] %s\n", update.Message.From.UserName, update.Message.Text)

		// свитч на обработку комманд
		// комманда - сообщение, начинающееся с "/"
		switch update.Message.Command() {

		// выводим информацию о боте
		case "start":
			reply = HelpMsg
		case "help":
			reply = HelpMsg

		// выводим информацию о мастерноде(валидаторе!)
		case "node_info":
			if update.Message.CommandArguments() == "" {
				oUsr := getUser(update.Message.Chat.ID)
				if oUsr.PubKey != "" {
					cndI := getValidInfo(oUsr.PubKey)
					chekIt := "нет"
					if oUsr.Notification == true {
						chekIt = "да"
					}
					reply = fmt.Sprintf("Ключ: %s\nАдрес: %s\nПрив.ключ: %s\nСтатус: %s\nКомиссия: %d%%\nСтэк: %f\nОповещение: %s", getMinString(oUsr.PubKey), getMinString(oUsr.UserAddress), getMinString(oUsr.PrivKey), getNodeStatusString(cndI.StatusInt), cndI.Commission, cndI.TotalStake32, chekIt)
				} else {
					if oUsr.ChatID == 0 {
						reply = "Добавьте мастерноду для слежения, командой /node_add"
					} else {
						reply = "Добавьте мастерноду для слежения, командой /node_edit"
					}
				}
			} else {
				// TODO: надо еще проверять формат pubkey!!! или вообще в списке мастернод-кандидатов, прежде чем в базу добавлять
				resSrch := searchValid(update.Message.CommandArguments())
				amntRes := len(resSrch)

				bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, fmt.Sprintf("Найдено мастернод: %d", amntRes)))

				for iN, oNd := range resSrch {
					reply = fmt.Sprintf("= Мастернода %d ==========\nКлюч: %s\nСтатус: %s\nКомиссия: %d%%\nСтэк: %f", (iN + 1), oNd.PubKey, getNodeStatusString(oNd.StatusInt), oNd.Commission, oNd.TotalStake32)
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, reply)

					btnKeyboard := tgbotapi.NewInlineKeyboardMarkup(
						tgbotapi.NewInlineKeyboardRow(
							tgbotapi.NewInlineKeyboardButtonSwitch("Ключ", oNd.PubKey),
						),
					)

					msg.ReplyMarkup = &btnKeyboard
					bot.Send(msg)
				}
				continue
			}

		// добавить мастерноду в список мониторинга
		case "node_add":
			oUsr := getUser(update.Message.Chat.ID)
			//if oUsr.PubKey == "" {
			if oUsr.ChatID == 0 {
				if update.Message.CommandArguments() == "" {
					reply = "Неправильный формат команды. Должен быть /node_add [pubkey], где pubkey-публичный ключ добавляемой мастерноды\n" +
						"или (!-только если доверяете нам) /node_add [pubkey] [usradr] [privkey], где usradr-адрес пользователя и privkey-приватный ключ"
				} else {
					fmt.Println("node_add")
					fmt.Println(update.Message.CommandArguments())

					arguments := strings.Split(update.Message.CommandArguments(), " ")
					argLen := len(arguments)

					fmt.Printf("%#v\n", arguments)
					fmt.Printf("ВСЕГО %d\n", argLen)

					// аргументов или 1 или 3!
					if argLen == 0 || argLen == 2 || argLen > 4 {
						reply = "Неправильный формат команды. Должен быть /node_add [pubkey], где pubkey-публичный ключ добавляемой мастерноды\n" +
							"или (!-только если доверяете нам) /node_add [pubkey] [usradr] [privkey], где usradr-адрес пользователя и privkey-приватный ключ"
					} else if argLen == 1 {
						// TODO: надо еще проверять формат pubkey!!! или вообще в списке мастернод-кандидатов, прежде чем в базу добавлять
						usr1 := usrData{
							PubKey:       arguments[0],
							UserName:     update.Message.From.UserName,
							ChatID:       update.Message.Chat.ID,
							Notification: true,
						}
						addUser(session, usr1)
						reply = "Мастернода успешно привязана к Вам."
					} else if argLen == 3 {
						// TODO: надо еще проверять формат pubkey и privkey!!! или вообще в списке мастернод-кандидатов, прежде чем в базу добавлять
						usr1 := usrData{
							PubKey:       arguments[0],
							UserAddress:  arguments[1],
							PrivKey:      arguments[2],
							UserName:     update.Message.From.UserName,
							ChatID:       update.Message.Chat.ID,
							Notification: true,
						}
						addUser(session, usr1)
						reply = "Мастернода успешно привязана к Вам."
					}
				}
			} else {
				reply = "Мастернода уже привязана к вам. Если хотите изменить, воспользуйтесь командой /node_edit"
			}
		// изменить pubkey у мастерноды
		case "node_edit":
			oUsr := getUser(update.Message.Chat.ID)
			if oUsr.ChatID != 0 {
				if update.Message.CommandArguments() == "" {
					reply = "Неправильный формат команды. Должен быть /node_edit [pubkey], где pubkey-публичный ключ мастерноды\n" +
						"или (!-только если доверяете нам) /node_edit [pubkey] [usradr] [privkey], где usradr-адрес пользователя и privkey-приватный ключ"
				} else {
					fmt.Println("node_edit")
					fmt.Println(update.Message.CommandArguments())
					arguments := strings.Split(update.Message.CommandArguments(), " ")
					argLen := len(arguments)
					fmt.Printf("%#v\n", arguments)
					fmt.Printf("ВСЕГО %d\n", argLen)

					// аргументов или 1 или 3!
					if argLen == 0 || argLen == 2 || argLen > 4 {
						reply = "Неправильный формат команды. Должен быть /node_edit [pubkey], где pubkey-публичный ключ мастерноды\n" +
							"или (!-только если доверяете нам) /node_edit [pubkey] [usradr] [privkey], где usradr-адрес пользователя и privkey-приватный ключ"
					} else if argLen == 1 {
						// TODO: надо еще проверять формат pubkey!!! или вообще в списке мастернод-кандидатов, прежде чем в базу добавлять
						usr1 := usrData{ChatID: update.Message.Chat.ID, PubKey: arguments[0]}
						editUserKey(session, usr1)
						reply = "Мастернода успешно изменена. Изменен [pubkey]."
					} else if argLen == 3 {
						usr1 := usrData{ChatID: update.Message.Chat.ID, PubKey: arguments[0], UserAddress: arguments[1], PrivKey: arguments[2]}
						editUserKey(session, usr1)
						reply = "Мастернода успешно изменена. Изменены [pubkey], [usradr] и [privkey] ."
					}
				}
			} else {
				reply = "Мастернода ещё не привязана к вам. Воспользуйтесь командой /node_add"
			}
		// удаление мастерноды
		case "node_del":
			oUsr := getUser(update.Message.Chat.ID)
			delNode(session, oUsr.ChatID)
			reply = "Мастернода отвязана"
		// изменить статус уведомления да/нет
		case "notification":
			oUsr := getUser(update.Message.Chat.ID)
			reply = editNodeNotif(session, oUsr.ChatID)

		//FIXME: вспомогательная команда - для теста
		/*case "cleandb":
			cleanDB(session)
			reply = "База очищена"*/
		// вкл/откл мастерноду
		case "candidate":
			oUsr := getUser(update.Message.Chat.ID)
			if oUsr.PrivKey != "" {
				argument := update.Message.CommandArguments()

				if argument == "" {
					reply = "Неправильный формат команды. Не уазано состояние в которое нужно перевести мастерноду:\n" +
						"on или 1 - включить, off или 0 - выключить"
				} else {
					statusMnode := false
					badCommand := false
					if argument == "1" || argument == "On" || argument == "on" || argument == "ON" {
						statusMnode = true
					} else if argument == "0" || argument == "Off" || argument == "off" || argument == "OFF" {
						statusMnode = false
					} else {
						badCommand = true
					}
					if badCommand != true {
						// Посылаем транзакцию
						tx, err := SetCandidateTransaction(oUsr.UserAddress, oUsr.PrivKey, oUsr.PubKey, statusMnode)
						if err != nil {
							reply = fmt.Sprintf("Произошла ошибка: %s", err.Error())
						} else {
							reply = fmt.Sprintf("Состояние мастерноды успешно изменено.\nТранзакция: %s", tx)
						}
					} else {
						reply = "Неправильный формат команды. Не уазано состояние в которое нужно перевести мастерноду:\n" +
							"on или 1 - включить, off или 0 - выключить"
					}
				}
			} else {
				if oUsr.ChatID != 0 {
					reply = "Не указан приватный ключ. Воспользуйтесь командой /node_edit"
				} else {
					reply = "Не указан приватный ключ. Воспользуйтесь командой /node_add"
				}
			}
		}

		msg := tgbotapi.NewMessage(update.Message.Chat.ID, reply)
		_, err = bot.Send(msg)
		if err != nil {
			fmt.Println("Ошибка отправки сообщения:", err)
		}
	}
}
