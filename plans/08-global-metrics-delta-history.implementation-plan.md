# Global Metrics Delta History — Implementation Plan (Backend)

## Статус

- ✅ Завершено.

## Задача

- Превратить MegaBack history endpoint в минимальный admin-only proxy глобальной истории Flatline.
- Убрать frontend-список сервисов, fan-out и вычисление фиксированных окон.
- Передавать три клиентские lower bound без изменения смысла.

## Исходное состояние

- Endpoint поддерживает старый запрос одного сервиса и новый запрос списка сервисов.
- Глобальный запрос запускает параллельный Flatline-запрос для каждого frontend-сервиса.
- Один медленный сервис задерживает или роняет весь ответ.
- Нижние границы minute/hour/day вычисляются в MegaBack от `latestBucket`.

## Решение

- Оставить один admin-only history-контракт.
- Требовать три положительные inclusive lower bound: minute, hour и day.
- Выполнять один Flatline-запрос и возвращать его глобальный результат.
- Не хранить и не вычислять список сервисов, retention-окна и latest bucket.
- Ошибка валидации возвращает client error; ошибка Flatline — gateway error.

## Чеклист

- ✅ Заменить оба старых режима одним глобальным handler.
- ✅ Упростить Flatline client до одного history-вызова без service/names.
- ✅ Удалить fan-out и связанные парсеры.
- ✅ Обновить handler и client тесты под новый контракт.
- ✅ Проверить, что один browser request создаёт один Flatline request.
- ✅ Прогнать Go tests.
