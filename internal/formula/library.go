package formula

import (
	"math"
	"strings"
	"time"
	"unicode"
)

// FunctionDoc describes one supported function so the product can list what the
// engine understands instead of leaving people to discover #NAME? by trial.
type FunctionDoc struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Syntax   string `json:"syntax"`
	Summary  string `json:"summary"`
}

var catalog = []FunctionDoc{
	{"SUM", "수학", "SUM(값1, 값2, …)", "숫자와 범위의 합계를 구합니다."},
	{"AVERAGE", "수학", "AVERAGE(값1, 값2, …)", "숫자의 평균을 구합니다."},
	{"MIN", "수학", "MIN(값1, 값2, …)", "가장 작은 숫자를 반환합니다."},
	{"MAX", "수학", "MAX(값1, 값2, …)", "가장 큰 숫자를 반환합니다."},
	{"MEDIAN", "수학", "MEDIAN(값1, 값2, …)", "숫자의 중앙값을 구합니다."},
	{"PRODUCT", "수학", "PRODUCT(값1, 값2, …)", "숫자를 모두 곱합니다."},
	{"ROUND", "수학", "ROUND(숫자, [자릿수])", "지정한 자릿수로 반올림합니다."},
	{"ROUNDUP", "수학", "ROUNDUP(숫자, [자릿수])", "지정한 자릿수로 올림합니다."},
	{"ROUNDDOWN", "수학", "ROUNDDOWN(숫자, [자릿수])", "지정한 자릿수로 내림합니다."},
	{"ABS", "수학", "ABS(숫자)", "절댓값을 반환합니다."},
	{"INT", "수학", "INT(숫자)", "가장 가까운 작은 정수로 내립니다."},
	{"MOD", "수학", "MOD(숫자, 나눌 수)", "나눗셈의 나머지를 반환합니다."},
	{"POWER", "수학", "POWER(밑, 지수)", "거듭제곱을 계산합니다."},
	{"SQRT", "수학", "SQRT(숫자)", "제곱근을 반환합니다."},
	{"COUNT", "집계", "COUNT(값1, 값2, …)", "숫자가 들어 있는 셀의 개수를 셉니다."},
	{"COUNTA", "집계", "COUNTA(값1, 값2, …)", "비어 있지 않은 셀의 개수를 셉니다."},
	{"COUNTBLANK", "집계", "COUNTBLANK(범위)", "비어 있는 셀의 개수를 셉니다."},
	{"SUMIF", "집계", "SUMIF(범위, 조건, [합계 범위])", "조건을 만족하는 값의 합계를 구합니다."},
	{"SUMIFS", "집계", "SUMIFS(합계 범위, 범위1, 조건1, …)", "여러 조건을 만족하는 값의 합계를 구합니다."},
	{"COUNTIF", "집계", "COUNTIF(범위, 조건)", "조건을 만족하는 셀의 개수를 셉니다."},
	{"COUNTIFS", "집계", "COUNTIFS(범위1, 조건1, …)", "여러 조건을 만족하는 셀의 개수를 셉니다."},
	{"IF", "논리", "IF(조건, 참일 때, [거짓일 때])", "조건에 따라 다른 값을 반환합니다."},
	{"IFERROR", "논리", "IFERROR(값, 오류일 때)", "값이 오류이면 대체 값을 반환합니다."},
	{"AND", "논리", "AND(조건1, 조건2, …)", "모든 조건이 참이면 TRUE입니다."},
	{"OR", "논리", "OR(조건1, 조건2, …)", "조건 중 하나라도 참이면 TRUE입니다."},
	{"NOT", "논리", "NOT(조건)", "참과 거짓을 뒤집습니다."},
	{"CONCAT", "텍스트", "CONCAT(값1, 값2, …)", "텍스트를 이어 붙입니다."},
	{"CONCATENATE", "텍스트", "CONCATENATE(값1, 값2, …)", "텍스트를 이어 붙입니다. CONCAT의 옛 이름입니다."},
	{"TEXTJOIN", "텍스트", "TEXTJOIN(구분자, 빈 값 무시, 값1, …)", "구분자를 넣어 텍스트를 이어 붙입니다."},
	{"LEFT", "텍스트", "LEFT(텍스트, [개수])", "왼쪽에서 지정한 개수만큼 잘라냅니다."},
	{"RIGHT", "텍스트", "RIGHT(텍스트, [개수])", "오른쪽에서 지정한 개수만큼 잘라냅니다."},
	{"MID", "텍스트", "MID(텍스트, 시작 위치, 개수)", "가운데 일부를 잘라냅니다."},
	{"LEN", "텍스트", "LEN(텍스트)", "글자 수를 셉니다."},
	{"TRIM", "텍스트", "TRIM(텍스트)", "앞뒤 공백과 중복 공백을 제거합니다."},
	{"UPPER", "텍스트", "UPPER(텍스트)", "영문을 대문자로 바꿉니다."},
	{"LOWER", "텍스트", "LOWER(텍스트)", "영문을 소문자로 바꿉니다."},
	{"PROPER", "텍스트", "PROPER(텍스트)", "각 단어의 첫 글자를 대문자로 바꿉니다."},
	{"SUBSTITUTE", "텍스트", "SUBSTITUTE(텍스트, 찾을 값, 바꿀 값, [몇 번째])", "텍스트의 일부를 바꿉니다. 몇 번째를 적으면 그 하나만 바꿉니다."},
	{"FIND", "텍스트", "FIND(찾을 값, 텍스트, [시작 위치])", "대소문자를 구분해 위치를 찾습니다."},
	{"SEARCH", "텍스트", "SEARCH(찾을 값, 텍스트, [시작 위치])", "대소문자를 구분하지 않고 위치를 찾습니다."},
	{"REPT", "텍스트", "REPT(텍스트, 횟수)", "텍스트를 지정한 횟수만큼 반복합니다."},
	{"VALUE", "텍스트", "VALUE(텍스트)", "숫자 형태의 텍스트를 숫자로 바꿉니다."},
	{"HYPERLINK", "텍스트", "HYPERLINK(주소, [표시할 텍스트])", "링크 주소와 표시 텍스트를 만듭니다."},
	{"DATE", "날짜", "DATE(연, 월, 일)", "연·월·일로 날짜를 만듭니다."},
	{"TODAY", "날짜", "TODAY()", "오늘 날짜를 반환합니다."},
	{"NOW", "날짜", "NOW()", "현재 날짜와 시간을 반환합니다."},
	{"YEAR", "날짜", "YEAR(날짜)", "날짜에서 연도를 꺼냅니다."},
	{"MONTH", "날짜", "MONTH(날짜)", "날짜에서 월을 꺼냅니다."},
	{"DAY", "날짜", "DAY(날짜)", "날짜에서 일을 꺼냅니다."},
	{"WEEKDAY", "날짜", "WEEKDAY(날짜, [유형])", "요일 번호를 반환합니다. 기본은 일요일이 1이고, 유형 2는 월요일이 1입니다."},
	{"VLOOKUP", "조회", "VLOOKUP(찾을 값, 범위, 열 번호, [정렬 여부])", "세로 방향으로 값을 찾습니다."},
	{"HLOOKUP", "조회", "HLOOKUP(찾을 값, 범위, 행 번호, [정렬 여부])", "가로 방향으로 값을 찾습니다."},
	{"INDEX", "조회", "INDEX(범위, 행, [열])", "범위에서 위치로 값을 꺼냅니다."},
	{"MATCH", "조회", "MATCH(찾을 값, 범위, [유형])", "값이 있는 위치를 반환합니다."},
	{"FILTER", "배열", "FILTER(범위, 조건1, …)", "조건을 만족하는 행만 남깁니다."},
	{"GROUPBY", "배열", "GROUPBY(이름표, 값, 함수, [머리글], [총계], [정렬])", "이름표별로 묶어 집계한 표를 만듭니다."},
	{"PIVOTBY", "배열", "PIVOTBY(행 이름표, 열 이름표, 값, 함수, [머리글], [총계], [정렬])", "행과 열 이름표로 교차 집계한 표를 만듭니다."},
	{"SORT", "배열", "SORT(범위, [정렬 열], [오름차순])", "범위를 정렬한 결과를 반환합니다."},
	{"CEILING", "수학", "CEILING(숫자, [배수])", "지정한 배수의 위쪽으로 올림합니다."},
	{"FLOOR", "수학", "FLOOR(숫자, [배수])", "지정한 배수의 아래쪽으로 내림합니다."},
	{"MROUND", "수학", "MROUND(숫자, 배수)", "가장 가까운 배수로 반올림합니다."},
	{"TRUNC", "수학", "TRUNC(숫자, [자릿수])", "반올림 없이 자릿수를 잘라냅니다."},
	{"SIGN", "수학", "SIGN(숫자)", "부호를 -1, 0, 1로 반환합니다."},
	{"EXP", "수학", "EXP(숫자)", "e의 거듭제곱을 계산합니다."},
	{"LN", "수학", "LN(숫자)", "자연로그를 계산합니다."},
	{"LOG", "수학", "LOG(숫자, [밑])", "지정한 밑의 로그를 계산합니다."},
	{"LOG10", "수학", "LOG10(숫자)", "상용로그를 계산합니다."},
	{"PI", "수학", "PI()", "원주율을 반환합니다."},
	{"QUOTIENT", "수학", "QUOTIENT(피제수, 제수)", "나눗셈의 몫만 정수로 반환합니다."},
	{"GCD", "수학", "GCD(값1, 값2, …)", "최대공약수를 구합니다."},
	{"LCM", "수학", "LCM(값1, 값2, …)", "최소공배수를 구합니다."},
	{"SUMSQ", "수학", "SUMSQ(값1, 값2, …)", "제곱의 합을 구합니다."},
	{"FACT", "수학", "FACT(숫자)", "계승(팩토리얼)을 계산합니다."},
	{"COMBIN", "수학", "COMBIN(n, k)", "조합의 수를 구합니다."},
	{"PERMUT", "수학", "PERMUT(n, k)", "순열의 수를 구합니다."},
	{"EVEN", "수학", "EVEN(숫자)", "가장 가까운 짝수로 올립니다."},
	{"ODD", "수학", "ODD(숫자)", "가장 가까운 홀수로 올립니다."},
	{"RADIANS", "수학", "RADIANS(각도)", "도를 라디안으로 바꿉니다."},
	{"DEGREES", "수학", "DEGREES(라디안)", "라디안을 도로 바꿉니다."},
	{"SIN", "수학", "SIN(라디안)", "사인 값을 반환합니다."},
	{"COS", "수학", "COS(라디안)", "코사인 값을 반환합니다."},
	{"TAN", "수학", "TAN(라디안)", "탄젠트 값을 반환합니다."},
	{"ASIN", "수학", "ASIN(숫자)", "아크사인 값을 반환합니다."},
	{"ACOS", "수학", "ACOS(숫자)", "아크코사인 값을 반환합니다."},
	{"ATAN", "수학", "ATAN(숫자)", "아크탄젠트 값을 반환합니다."},
	{"ATAN2", "수학", "ATAN2(x, y)", "좌표의 각도를 라디안으로 반환합니다."},
	{"SINH", "수학", "SINH(숫자)", "쌍곡사인 값을 반환합니다."},
	{"COSH", "수학", "COSH(숫자)", "쌍곡코사인 값을 반환합니다."},
	{"TANH", "수학", "TANH(숫자)", "쌍곡탄젠트 값을 반환합니다."},
	{"RAND", "수학", "RAND()", "0 이상 1 미만의 난수를 반환합니다. 변경이 있을 때마다 다시 계산합니다."},
	{"RANDBETWEEN", "수학", "RANDBETWEEN(하한, 상한)", "두 정수 사이의 난수를 반환합니다."},
	{"SUMPRODUCT", "집계", "SUMPRODUCT(범위1, 범위2, …)", "같은 위치끼리 곱한 값을 모두 더합니다."},
	{"SUBTOTAL", "집계", "SUBTOTAL(함수 번호, 범위, …)", "1~11 또는 101~111 번호로 집계 방법을 골라 계산합니다."},
	{"AGGREGATE", "집계", "AGGREGATE(함수 번호, 옵션, 범위, …)", "집계하면서 오류가 든 칸을 건너뛸 수 있습니다. 옵션 6이 오류를 건너뜁니다."},
	{"AVERAGEIF", "집계", "AVERAGEIF(범위, 조건, [평균 범위])", "조건을 만족하는 값의 평균을 구합니다."},
	{"AVERAGEIFS", "집계", "AVERAGEIFS(평균 범위, 범위1, 조건1, …)", "여러 조건을 만족하는 값의 평균을 구합니다."},
	{"MAXIFS", "집계", "MAXIFS(값 범위, 범위1, 조건1, …)", "조건을 만족하는 값 중 최댓값을 구합니다."},
	{"MINIFS", "집계", "MINIFS(값 범위, 범위1, 조건1, …)", "조건을 만족하는 값 중 최솟값을 구합니다."},
	{"COUNTUNIQUE", "집계", "COUNTUNIQUE(값1, 값2, …)", "서로 다른 값의 개수를 셉니다."},
	{"AVERAGEA", "집계", "AVERAGEA(값1, 값2, …)", "텍스트를 0으로 보고 평균을 구합니다."},
	{"STDEV", "통계", "STDEV(값1, 값2, …)", "표본 표준편차를 구합니다."},
	{"STDEVP", "통계", "STDEVP(값1, 값2, …)", "모집단 표준편차를 구합니다."},
	{"VAR", "통계", "VAR(값1, 값2, …)", "표본 분산을 구합니다."},
	{"VARP", "통계", "VARP(값1, 값2, …)", "모집단 분산을 구합니다."},
	{"STDEVA", "통계", "STDEVA(값1, 값2, …)", "텍스트를 0으로 보고 표본 표준편차를 구합니다."},
	{"STDEVPA", "통계", "STDEVPA(값1, 값2, …)", "텍스트를 0으로 보고 모집단 표준편차를 구합니다."},
	{"VARA", "통계", "VARA(값1, 값2, …)", "텍스트를 0으로 보고 표본 분산을 구합니다."},
	{"VARPA", "통계", "VARPA(값1, 값2, …)", "텍스트를 0으로 보고 모집단 분산을 구합니다."},
	{"LARGE", "통계", "LARGE(범위, k)", "k번째로 큰 값을 반환합니다."},
	{"SMALL", "통계", "SMALL(범위, k)", "k번째로 작은 값을 반환합니다."},
	{"RANK", "통계", "RANK(값, 범위, [오름차순])", "값의 순위를 구합니다."},
	{"PERCENTILE", "통계", "PERCENTILE(범위, 비율)", "지정한 백분위 값을 구합니다."},
	{"QUARTILE", "통계", "QUARTILE(범위, 0~4)", "사분위 값을 구합니다."},
	// 엑셀 2010 부터 쓰는 점 붙은 이름들. 셈은 위와 같고 이름만 다르다.
	{"STDEV.S", "통계", "STDEV.S(값1, 값2, …)", "표본 표준편차를 구합니다. STDEV 와 같습니다."},
	{"STDEV.P", "통계", "STDEV.P(값1, 값2, …)", "모집단 표준편차를 구합니다. STDEVP 와 같습니다."},
	{"VAR.S", "통계", "VAR.S(값1, 값2, …)", "표본 분산을 구합니다. VAR 와 같습니다."},
	{"VAR.P", "통계", "VAR.P(값1, 값2, …)", "모집단 분산을 구합니다. VARP 와 같습니다."},
	{"MODE.SNGL", "통계", "MODE.SNGL(값1, 값2, …)", "가장 자주 나오는 값을 구합니다. MODE 와 같습니다."},
	{"RANK.EQ", "통계", "RANK.EQ(값, 범위, [순서])", "등수를 구하고 동점은 모두 높은 등수를 받습니다. RANK 와 같습니다."},
	{"PERCENTILE.INC", "통계", "PERCENTILE.INC(범위, 0~1)", "백분위 값을 구합니다. PERCENTILE 과 같습니다."},
	{"QUARTILE.INC", "통계", "QUARTILE.INC(범위, 0~4)", "사분위 값을 구합니다. QUARTILE 과 같습니다."},
	{"FORECAST.LINEAR", "통계", "FORECAST.LINEAR(x, y범위, x범위)", "선형 추세로 예측합니다. FORECAST 와 같습니다."},
	{"COVARIANCE.P", "통계", "COVARIANCE.P(범위1, 범위2)", "모집단 공분산을 구합니다. COVAR 와 같습니다."},
	// 아래는 셈이 달라서 별명이 아니다.
	{"PERCENTILE.EXC", "통계", "PERCENTILE.EXC(범위, 0~1)", "양 끝을 자료 밖에 두는 백분위 값을 구합니다. 0과 1에 가까운 값은 #NUM! 입니다."},
	{"QUARTILE.EXC", "통계", "QUARTILE.EXC(범위, 1~3)", "양 끝을 자료 밖에 두는 사분위 값을 구합니다. 0과 4는 #NUM! 입니다."},
	{"RANK.AVG", "통계", "RANK.AVG(값, 범위, [순서])", "등수를 구하고 동점은 그들이 차지한 등수의 평균을 받습니다."},
	{"MAXA", "통계", "MAXA(값1, 값2, …)", "최댓값을 구하되 숫자가 아닌 값을 0으로 셉니다."},
	{"MINA", "통계", "MINA(값1, 값2, …)", "최솟값을 구하되 숫자가 아닌 값을 0으로 셉니다."},
	// 표에서 조건에 맞는 줄만 골라 셈하는 함수들. 표와 조건표 모두 첫 줄이
	// 머리글이다.
	{"DSUM", "데이터베이스", "DSUM(표, 열, 조건표)", "조건에 맞는 줄의 합계를 구합니다."},
	{"DAVERAGE", "데이터베이스", "DAVERAGE(표, 열, 조건표)", "조건에 맞는 줄의 평균을 구합니다."},
	{"DCOUNT", "데이터베이스", "DCOUNT(표, 열, 조건표)", "조건에 맞는 줄에서 숫자의 개수를 셉니다."},
	{"DCOUNTA", "데이터베이스", "DCOUNTA(표, 열, 조건표)", "조건에 맞는 줄에서 비어 있지 않은 칸을 셉니다."},
	{"DMAX", "데이터베이스", "DMAX(표, 열, 조건표)", "조건에 맞는 줄의 최댓값을 구합니다."},
	{"DMIN", "데이터베이스", "DMIN(표, 열, 조건표)", "조건에 맞는 줄의 최솟값을 구합니다."},
	{"DPRODUCT", "데이터베이스", "DPRODUCT(표, 열, 조건표)", "조건에 맞는 줄의 곱을 구합니다."},
	{"DSTDEV", "데이터베이스", "DSTDEV(표, 열, 조건표)", "조건에 맞는 줄의 표본 표준편차를 구합니다."},
	{"DSTDEVP", "데이터베이스", "DSTDEVP(표, 열, 조건표)", "조건에 맞는 줄의 모집단 표준편차를 구합니다."},
	{"DVAR", "데이터베이스", "DVAR(표, 열, 조건표)", "조건에 맞는 줄의 표본 분산을 구합니다."},
	{"DVARP", "데이터베이스", "DVARP(표, 열, 조건표)", "조건에 맞는 줄의 모집단 분산을 구합니다."},
	{"DGET", "데이터베이스", "DGET(표, 열, 조건표)", "조건에 맞는 줄이 딱 하나일 때 그 값을 구합니다."},
	// 진법과 비트. 진법 값은 10자리 2의 보수라 맨 앞자리가 1이면 음수다.
	{"BIN2DEC", "공학", "BIN2DEC(2진수)", "2진수를 10진수로 바꿉니다."},
	{"BIN2OCT", "공학", "BIN2OCT(2진수, [자릿수])", "2진수를 8진수로 바꿉니다."},
	{"BIN2HEX", "공학", "BIN2HEX(2진수, [자릿수])", "2진수를 16진수로 바꿉니다."},
	{"OCT2DEC", "공학", "OCT2DEC(8진수)", "8진수를 10진수로 바꿉니다."},
	{"OCT2BIN", "공학", "OCT2BIN(8진수, [자릿수])", "8진수를 2진수로 바꿉니다."},
	{"OCT2HEX", "공학", "OCT2HEX(8진수, [자릿수])", "8진수를 16진수로 바꿉니다."},
	{"HEX2DEC", "공학", "HEX2DEC(16진수)", "16진수를 10진수로 바꿉니다."},
	{"HEX2BIN", "공학", "HEX2BIN(16진수, [자릿수])", "16진수를 2진수로 바꿉니다."},
	{"HEX2OCT", "공학", "HEX2OCT(16진수, [자릿수])", "16진수를 8진수로 바꿉니다."},
	{"DEC2BIN", "공학", "DEC2BIN(10진수, [자릿수])", "10진수를 2진수로 바꿉니다."},
	{"DEC2OCT", "공학", "DEC2OCT(10진수, [자릿수])", "10진수를 8진수로 바꿉니다."},
	{"DEC2HEX", "공학", "DEC2HEX(10진수, [자릿수])", "10진수를 16진수로 바꿉니다."},
	{"BITAND", "공학", "BITAND(수1, 수2)", "두 수의 비트 AND를 구합니다."},
	{"BITOR", "공학", "BITOR(수1, 수2)", "두 수의 비트 OR를 구합니다."},
	{"BITXOR", "공학", "BITXOR(수1, 수2)", "두 수의 비트 XOR를 구합니다."},
	{"BITLSHIFT", "공학", "BITLSHIFT(수, 자릿수)", "비트를 왼쪽으로 밉니다."},
	{"BITRSHIFT", "공학", "BITRSHIFT(수, 자릿수)", "비트를 오른쪽으로 밉니다."},
	{"DELTA", "공학", "DELTA(수1, [수2])", "두 수가 같으면 1, 다르면 0을 냅니다."},
	{"GESTEP", "공학", "GESTEP(수, [기준])", "수가 기준 이상이면 1, 아니면 0을 냅니다."},
	{"ERF", "공학", "ERF(하한, [상한])", "오차 함수 값을 구합니다."},
	{"ERFC", "공학", "ERFC(하한)", "여오차 함수 값을 구합니다."},
	// 삼각의 역수와 쌍곡선. 나누는 쪽이 0 이면 #DIV/0! 이다.
	{"SEC", "수학", "SEC(각도)", "코사인의 역수를 구합니다."},
	{"CSC", "수학", "CSC(각도)", "사인의 역수를 구합니다."},
	{"COT", "수학", "COT(각도)", "탄젠트의 역수를 구합니다."},
	{"SECH", "수학", "SECH(수)", "쌍곡코사인의 역수를 구합니다."},
	{"CSCH", "수학", "CSCH(수)", "쌍곡사인의 역수를 구합니다."},
	{"COTH", "수학", "COTH(수)", "쌍곡탄젠트의 역수를 구합니다."},
	{"ACOT", "수학", "ACOT(수)", "역코탄젠트를 0과 파이 사이의 값으로 구합니다."},
	{"ACOTH", "수학", "ACOTH(수)", "역쌍곡코탄젠트를 구합니다. -1과 1 사이는 답이 없습니다."},
	{"ACOSH", "수학", "ACOSH(수)", "역쌍곡코사인을 구합니다. 1 이상이어야 합니다."},
	{"ASINH", "수학", "ASINH(수)", "역쌍곡사인을 구합니다."},
	{"ATANH", "수학", "ATANH(수)", "역쌍곡탄젠트를 구합니다. -1과 1 사이여야 합니다."},
	{"GAMMALN", "수학", "GAMMALN(수)", "감마 함수의 자연로그를 구합니다."},
	{"SQRTPI", "수학", "SQRTPI(수)", "수에 파이를 곱한 값의 제곱근을 구합니다."},
	// 자리 올림의 정밀한 갈래. 음수에서 갈린다.
	{"CEILING.MATH", "수학", "CEILING.MATH(수, [기준], [방식])", "올림합니다. 방식을 주면 음수가 0에서 멀어지는 쪽으로 갑니다."},
	{"FLOOR.MATH", "수학", "FLOOR.MATH(수, [기준], [방식])", "내림합니다. 방식을 주면 음수가 0 쪽으로 갑니다."},
	{"CEILING.PRECISE", "수학", "CEILING.PRECISE(수, [기준])", "언제나 위로 올립니다. 기준의 부호는 무시합니다."},
	{"FLOOR.PRECISE", "수학", "FLOOR.PRECISE(수, [기준])", "언제나 아래로 내립니다. 기준의 부호는 무시합니다."},
	{"ISO.CEILING", "수학", "ISO.CEILING(수, [기준])", "CEILING.PRECISE 와 같습니다."},
	// 조합과 제곱합.
	{"COMBINA", "수학", "COMBINA(전체, 뽑을 개수)", "되풀이를 허용한 조합의 수를 구합니다."},
	{"FACTDOUBLE", "수학", "FACTDOUBLE(수)", "이중 계승을 구합니다. 6이면 6×4×2입니다."},
	{"MULTINOMIAL", "수학", "MULTINOMIAL(수1, 수2, …)", "다항 계수를 구합니다."},
	{"SUMX2MY2", "수학", "SUMX2MY2(범위1, 범위2)", "짝지은 값의 제곱 차를 모두 더합니다."},
	{"SUMX2PY2", "수학", "SUMX2PY2(범위1, 범위2)", "짝지은 값의 제곱 합을 모두 더합니다."},
	{"SUMXMY2", "수학", "SUMXMY2(범위1, 범위2)", "짝지은 값의 차를 제곱해 모두 더합니다."},
	{"SERIESSUM", "수학", "SERIESSUM(x, n, m, 계수)", "거듭제곱 급수의 합을 구합니다."},
	{"BASE", "수학", "BASE(수, 진법, [자릿수])", "10진수를 2~36진법으로 바꿉니다."},
	{"DECIMAL", "수학", "DECIMAL(글자, 진법)", "2~36진법 글자를 10진수로 바꿉니다."},
	{"MUNIT", "수학", "MUNIT(크기)", "단위 행렬을 펼칩니다."},
	{"RANDARRAY", "수학", "RANDARRAY([행], [열], [최소], [최대], [정수])", "지정한 크기의 난수를 펼칩니다."},
	// 분포. 되돌리는 쪽(INV)은 0 과 1 사이의 확률을 받는다.
	{"NORMSDIST", "통계", "NORMSDIST(z)", "표준정규분포의 누적값을 구합니다."},
	{"NORMSINV", "통계", "NORMSINV(확률)", "표준정규분포의 누적값에서 z를 되돌립니다."},
	{"NORMDIST", "통계", "NORMDIST(수, 평균, 표준편차, [누적])", "정규분포 값을 구합니다."},
	{"NORMINV", "통계", "NORMINV(확률, 평균, 표준편차)", "정규분포의 누적값에서 수를 되돌립니다."},
	{"GAUSS", "통계", "GAUSS(z)", "평균과 z 사이의 확률을 구합니다."},
	{"LOGNORMDIST", "통계", "LOGNORMDIST(수, 평균, 표준편차)", "로그정규분포의 누적값을 구합니다."},
	{"LOGINV", "통계", "LOGINV(확률, 평균, 표준편차)", "로그정규분포의 누적값에서 수를 되돌립니다."},
	{"CONFIDENCE", "통계", "CONFIDENCE(유의수준, 표준편차, 표본 수)", "신뢰구간의 반너비를 구합니다."},
	{"STANDARDIZE", "통계", "STANDARDIZE(수, 평균, 표준편차)", "값을 표준점수로 바꿉니다."},
	{"FISHER", "통계", "FISHER(수)", "피셔 변환값을 구합니다."},
	{"FISHERINV", "통계", "FISHERINV(수)", "피셔 변환을 되돌립니다."},
	{"EXPONDIST", "통계", "EXPONDIST(수, 비율, 누적)", "지수분포 값을 구합니다."},
	{"POISSON", "통계", "POISSON(횟수, 평균, 누적)", "포아송분포 값을 구합니다."},
	{"BINOMDIST", "통계", "BINOMDIST(성공, 시행, 확률, 누적)", "이항분포 값을 구합니다."},
	{"NEGBINOMDIST", "통계", "NEGBINOMDIST(실패, 성공, 확률)", "음이항분포 값을 구합니다."},
	{"HYPGEOMDIST", "통계", "HYPGEOMDIST(뽑은 성공, 표본, 전체 성공, 전체)", "초기하분포 값을 구합니다."},
	{"CRITBINOM", "통계", "CRITBINOM(시행, 확률, 기준)", "누적 이항분포가 기준을 넘는 첫 횟수를 구합니다."},
	{"WEIBULL", "통계", "WEIBULL(수, 모양, 규모, 누적)", "와이블분포 값을 구합니다."},
	// 흩어진 정도.
	{"AVEDEV", "통계", "AVEDEV(값1, 값2, …)", "평균에서 떨어진 거리의 평균을 구합니다."},
	{"DEVSQ", "통계", "DEVSQ(값1, 값2, …)", "평균에서 떨어진 거리의 제곱합을 구합니다."},
	{"SKEW", "통계", "SKEW(값1, 값2, …)", "왜도를 구합니다."},
	{"KURT", "통계", "KURT(값1, 값2, …)", "첨도를 구합니다."},
	{"MODE.MULT", "통계", "MODE.MULT(값1, 값2, …)", "가장 자주 나온 값을 모두 펼칩니다."},
	{"PERCENTRANK", "통계", "PERCENTRANK(범위, 값, [자릿수])", "값이 범위에서 차지하는 백분위를 구합니다."},
	{"TRIMMEAN", "통계", "TRIMMEAN(범위, 비율)", "위아래를 덜어 내고 평균을 구합니다."},
	{"PROB", "통계", "PROB(값 범위, 확률 범위, 하한, [상한])", "값이 구간에 들 확률을 구합니다."},
	{"STEYX", "통계", "STEYX(y 범위, x 범위)", "회귀 예측값의 표준오차를 구합니다."},
	{"ZTEST", "통계", "ZTEST(범위, 값, [표준편차])", "z 검정의 단측 확률을 구합니다."},
	// 검정 분포. 어느 꼬리를 재는지가 함수마다 다르다.
	{"CHIDIST", "통계", "CHIDIST(수, 자유도)", "카이제곱분포의 상측확률을 구합니다."},
	{"CHIINV", "통계", "CHIINV(확률, 자유도)", "카이제곱분포의 상측확률에서 수를 되돌립니다."},
	{"CHITEST", "통계", "CHITEST(관측 범위, 기대 범위)", "카이제곱 적합도 검정의 확률을 구합니다."},
	{"TDIST", "통계", "TDIST(수, 자유도, 꼬리 수)", "t분포의 확률을 구합니다. 꼬리 수는 1 또는 2입니다."},
	{"TINV", "통계", "TINV(확률, 자유도)", "t분포의 양측 확률에서 수를 되돌립니다."},
	{"TTEST", "통계", "TTEST(범위1, 범위2, 꼬리 수, 종류)", "t 검정의 확률을 구합니다. 종류는 대응표본·등분산·웰치입니다."},
	{"FDIST", "통계", "FDIST(수, 자유도1, 자유도2)", "F분포의 상측확률을 구합니다."},
	{"FINV", "통계", "FINV(확률, 자유도1, 자유도2)", "F분포의 상측확률에서 수를 되돌립니다."},
	{"FTEST", "통계", "FTEST(범위1, 범위2)", "두 분산이 같은지 보는 F 검정의 양측 확률을 구합니다."},
	{"BETADIST", "통계", "BETADIST(수, 알파, 베타, [하한], [상한])", "베타분포의 누적값을 구합니다."},
	{"BETAINV", "통계", "BETAINV(확률, 알파, 베타, [하한], [상한])", "베타분포의 누적값에서 수를 되돌립니다."},
	// 채권과 단기 증권. 기준(basis)은 0~4 이며 YEARFRAC 과 같은 규칙이다.
	{"DISC", "재무", "DISC(결제일, 만기일, 가격, 상환액, [기준])", "할인율을 구합니다."},
	{"INTRATE", "재무", "INTRATE(결제일, 만기일, 투자액, 상환액, [기준])", "전액 투자한 증권의 이율을 구합니다."},
	{"RECEIVED", "재무", "RECEIVED(결제일, 만기일, 투자액, 할인율, [기준])", "만기에 받을 금액을 구합니다."},
	{"PRICEDISC", "재무", "PRICEDISC(결제일, 만기일, 할인율, 상환액, [기준])", "할인 발행 증권의 가격을 구합니다."},
	{"YIELDDISC", "재무", "YIELDDISC(결제일, 만기일, 가격, 상환액, [기준])", "할인 발행 증권의 수익률을 구합니다."},
	{"PRICEMAT", "재무", "PRICEMAT(결제일, 만기일, 발행일, 이율, 수익률, [기준])", "만기에 이자를 주는 증권의 가격을 구합니다."},
	{"YIELDMAT", "재무", "YIELDMAT(결제일, 만기일, 발행일, 이율, 가격, [기준])", "만기에 이자를 주는 증권의 수익률을 구합니다."},
	{"TBILLPRICE", "재무", "TBILLPRICE(결제일, 만기일, 할인율)", "단기 국채의 가격을 구합니다."},
	{"TBILLYIELD", "재무", "TBILLYIELD(결제일, 만기일, 가격)", "단기 국채의 수익률을 구합니다."},
	{"TBILLEQ", "재무", "TBILLEQ(결제일, 만기일, 할인율)", "단기 국채의 채권 등가 수익률을 구합니다."},
	{"DOLLARDE", "재무", "DOLLARDE(분수 표기, 분모)", "분수로 적은 가격을 소수로 바꿉니다."},
	{"DOLLARFR", "재무", "DOLLARFR(소수, 분모)", "소수로 적은 가격을 분수 표기로 바꿉니다."},
	{"FVSCHEDULE", "재무", "FVSCHEDULE(원금, 이율 목록)", "해마다 다른 이율을 차례로 적용한 미래가치를 구합니다."},
	{"ISPMT", "재무", "ISPMT(이율, 기간, 총 기간, 현재가치)", "원금 균등 상환에서 특정 기간의 이자를 구합니다."},
	// 이자 지급일과 쿠폰 채권. 횟수는 1(연), 2(반년), 4(분기)다.
	{"COUPPCD", "재무", "COUPPCD(결제일, 만기일, 횟수, [기준])", "결제일 직전의 이자 지급일을 구합니다."},
	{"COUPNCD", "재무", "COUPNCD(결제일, 만기일, 횟수, [기준])", "결제일 다음의 이자 지급일을 구합니다."},
	{"COUPNUM", "재무", "COUPNUM(결제일, 만기일, 횟수, [기준])", "만기까지 남은 이자 지급 횟수를 구합니다."},
	{"COUPDAYS", "재무", "COUPDAYS(결제일, 만기일, 횟수, [기준])", "결제일이 든 이자 기간의 길이를 구합니다."},
	{"COUPDAYBS", "재무", "COUPDAYBS(결제일, 만기일, 횟수, [기준])", "지난 지급일부터 결제일까지의 날 수를 구합니다."},
	{"COUPDAYSNC", "재무", "COUPDAYSNC(결제일, 만기일, 횟수, [기준])", "결제일부터 다음 지급일까지의 날 수를 구합니다."},
	{"PRICE", "재무", "PRICE(결제일, 만기일, 표면이율, 수익률, 상환액, 횟수, [기준])", "이자를 여러 번 주는 채권의 가격을 구합니다."},
	{"YIELD", "재무", "YIELD(결제일, 만기일, 표면이율, 가격, 상환액, 횟수, [기준])", "이자를 여러 번 주는 채권의 수익률을 구합니다."},
	{"DURATION", "재무", "DURATION(결제일, 만기일, 표면이율, 수익률, 횟수, [기준])", "맥컬레이 듀레이션을 구합니다."},
	{"MDURATION", "재무", "MDURATION(결제일, 만기일, 표면이율, 수익률, 횟수, [기준])", "수정 듀레이션을 구합니다."},
	{"ACCRINTM", "재무", "ACCRINTM(발행일, 결제일, 이율, 액면가, [기준])", "만기에 한 번 이자를 주는 증권의 경과이자를 구합니다."},
	// 바이트 단위 텍스트. 한글과 한자는 두 바이트로 센다.
	{"LENB", "텍스트", "LENB(문자열)", "글자 수를 바이트로 셉니다. 한글은 두 바이트입니다."},
	{"LEFTB", "텍스트", "LEFTB(문자열, [바이트 수])", "왼쪽에서 바이트 수만큼 잘라 냅니다."},
	{"RIGHTB", "텍스트", "RIGHTB(문자열, [바이트 수])", "오른쪽에서 바이트 수만큼 잘라 냅니다."},
	{"MIDB", "텍스트", "MIDB(문자열, 시작 바이트, 바이트 수)", "바이트 자리로 가운데를 잘라 냅니다."},
	{"FINDB", "텍스트", "FINDB(찾을 글자, 문자열, [시작 바이트])", "대소문자를 가려 찾고 바이트 자리를 냅니다."},
	{"SEARCHB", "텍스트", "SEARCHB(찾을 글자, 문자열, [시작 바이트])", "대소문자를 가리지 않고 찾고 바이트 자리를 냅니다."},
	{"REPLACEB", "텍스트", "REPLACEB(문자열, 시작 바이트, 바이트 수, 새 글자)", "바이트 자리로 바꿔 넣습니다."},
	{"ASC", "텍스트", "ASC(문자열)", "전각 글자를 반각으로 바꿉니다."},
	{"JIS", "텍스트", "JIS(문자열)", "반각 글자를 전각으로 바꿉니다."},
	{"DBCS", "텍스트", "DBCS(문자열)", "반각 글자를 전각으로 바꿉니다. JIS 와 같습니다."},
	{"ROMAN", "텍스트", "ROMAN(수, [형식])", "로마 숫자로 적습니다. 형식 0~4로 짧기를 고릅니다."},
	{"ARABIC", "텍스트", "ARABIC(로마 숫자)", "로마 숫자를 수로 읽습니다."},
	// 주말을 직접 정하는 날짜 셈.
	{"NETWORKDAYS.INTL", "날짜", "NETWORKDAYS.INTL(시작일, 종료일, [주말], [휴일])", "주말을 직접 정해 일하는 날을 셉니다."},
	{"WORKDAY.INTL", "날짜", "WORKDAY.INTL(시작일, 일수, [주말], [휴일])", "주말을 직접 정해 며칠 뒤 일하는 날을 구합니다."},
	{"EPOCHTODATE", "날짜", "EPOCHTODATE(초, [단위])", "1970년부터 센 시각을 날짜로 바꿉니다."},
	// 복소수. "3+4i" 같은 글자로 주고받는다.
	{"COMPLEX", "공학", "COMPLEX(실수부, 허수부, [단위])", "실수부와 허수부로 복소수를 만듭니다."},
	{"IMREAL", "공학", "IMREAL(복소수)", "실수부를 구합니다."},
	{"IMAGINARY", "공학", "IMAGINARY(복소수)", "허수부를 구합니다."},
	{"IMABS", "공학", "IMABS(복소수)", "복소수의 크기를 구합니다."},
	{"IMARGUMENT", "공학", "IMARGUMENT(복소수)", "복소수의 편각을 구합니다."},
	{"IMCONJUGATE", "공학", "IMCONJUGATE(복소수)", "켤레복소수를 구합니다."},
	{"IMSUM", "공학", "IMSUM(복소수1, 복소수2, …)", "복소수를 더합니다."},
	{"IMSUB", "공학", "IMSUB(복소수1, 복소수2)", "복소수를 뺍니다."},
	{"IMPRODUCT", "공학", "IMPRODUCT(복소수1, 복소수2, …)", "복소수를 곱합니다."},
	{"IMDIV", "공학", "IMDIV(복소수1, 복소수2)", "복소수를 나눕니다."},
	{"IMPOWER", "공학", "IMPOWER(복소수, 지수)", "복소수를 거듭제곱합니다."},
	{"IMSQRT", "공학", "IMSQRT(복소수)", "복소수의 제곱근을 구합니다."},
	{"IMEXP", "공학", "IMEXP(복소수)", "복소수의 지수를 구합니다."},
	{"IMLN", "공학", "IMLN(복소수)", "복소수의 자연로그를 구합니다."},
	{"IMLOG10", "공학", "IMLOG10(복소수)", "복소수의 상용로그를 구합니다."},
	{"IMLOG2", "공학", "IMLOG2(복소수)", "복소수의 밑이 2인 로그를 구합니다."},
	{"MODE", "통계", "MODE(값1, 값2, …)", "가장 자주 나오는 값을 반환합니다."},
	{"GEOMEAN", "통계", "GEOMEAN(값1, 값2, …)", "기하평균을 구합니다. 수익률 평균에 씁니다."},
	{"HARMEAN", "통계", "HARMEAN(값1, 값2, …)", "조화평균을 구합니다."},
	{"CORREL", "통계", "CORREL(범위1, 범위2)", "두 데이터의 상관계수를 구합니다."},
	{"RSQ", "통계", "RSQ(범위1, 범위2)", "결정계수 R²를 구합니다."},
	{"PEARSON", "통계", "PEARSON(y 범위, x 범위)", "피어슨 상관계수를 구합니다."},
	{"COVAR", "통계", "COVAR(범위1, 범위2)", "공분산을 구합니다."},
	{"SLOPE", "통계", "SLOPE(y 범위, x 범위)", "회귀직선의 기울기를 구합니다."},
	{"INTERCEPT", "통계", "INTERCEPT(y 범위, x 범위)", "회귀직선의 절편을 구합니다."},
	{"FORECAST", "통계", "FORECAST(x, y 범위, x 범위)", "선형 회귀로 값을 예측합니다."},
	{"TREND", "통계", "TREND(y 범위, [x 범위], [구할 x], [b])", "회귀 직선 위의 값을 구합니다. FORECAST와 인수 차례가 다릅니다."},
	{"PMT", "재무", "PMT(이율, 기간 수, 현재가치, [미래가치], [납입 시점])", "대출 상환금 등 정기 납입액을 구합니다."},
	{"IPMT", "재무", "IPMT(이율, 기간, 기간 수, 현재가치, [미래가치], [납입 시점])", "특정 회차의 이자 부분을 구합니다."},
	{"PPMT", "재무", "PPMT(이율, 기간, 기간 수, 현재가치, [미래가치], [납입 시점])", "특정 회차의 원금 부분을 구합니다."},
	{"FV", "재무", "FV(이율, 기간 수, 납입액, [현재가치], [납입 시점])", "미래가치를 구합니다."},
	{"PV", "재무", "PV(이율, 기간 수, 납입액, [미래가치], [납입 시점])", "현재가치를 구합니다."},
	{"NPER", "재무", "NPER(이율, 납입액, 현재가치, [미래가치], [납입 시점])", "상환에 필요한 기간 수를 구합니다."},
	{"RATE", "재무", "RATE(기간 수, 납입액, 현재가치, [미래가치], [납입 시점], [추정값])", "기간별 이율을 역산합니다."},
	{"NPV", "재무", "NPV(할인율, 현금흐름1, …)", "순현재가치를 구합니다."},
	{"IRR", "재무", "IRR(현금흐름 범위, [추정값])", "내부수익률을 구합니다."},
	{"MIRR", "재무", "MIRR(현금흐름 범위, 조달 이율, 재투자 이율)", "수정 내부수익률을 구합니다."},
	{"XNPV", "재무", "XNPV(할인율, 현금흐름 범위, 날짜 범위)", "날짜가 불규칙한 현금흐름의 순현재가치를 구합니다."},
	{"XIRR", "재무", "XIRR(현금흐름 범위, 날짜 범위, [추정값])", "날짜가 불규칙한 현금흐름의 내부수익률을 구합니다."},
	{"CUMIPMT", "재무", "CUMIPMT(이율, 기간 수, 현재가치, 시작 회차, 끝 회차, 납입 시점)", "구간 누적 이자를 구합니다."},
	{"CUMPRINC", "재무", "CUMPRINC(이율, 기간 수, 현재가치, 시작 회차, 끝 회차, 납입 시점)", "구간 누적 원금을 구합니다."},
	{"SLN", "재무", "SLN(취득가, 잔존가치, 내용연수)", "정액법 감가상각비를 구합니다."},
	{"SYD", "재무", "SYD(취득가, 잔존가치, 내용연수, 기간)", "연수합계법 감가상각비를 구합니다."},
	{"DDB", "재무", "DDB(취득가, 잔존가치, 내용연수, 기간, [배수])", "정률 체감법 감가상각비를 구합니다."},
	{"DB", "재무", "DB(취득가, 잔존가치, 내용연수, 기간, [배수])", "정률법 감가상각비를 구합니다."},
	{"EFFECT", "재무", "EFFECT(명목이율, 연 복리 횟수)", "실효 연이율을 구합니다."},
	{"NOMINAL", "재무", "NOMINAL(실효이율, 연 복리 횟수)", "명목 연이율을 구합니다."},
	{"RRI", "재무", "RRI(기간 수, 현재가치, 미래가치)", "성장에 필요한 기간별 수익률을 구합니다."},
	{"PDURATION", "재무", "PDURATION(이율, 현재가치, 목표가치)", "목표 금액까지 걸리는 기간을 구합니다."},
	{"IFS", "논리", "IFS(조건1, 값1, 조건2, 값2, …)", "여러 조건을 차례로 검사해 첫 참의 값을 반환합니다."},
	{"SWITCH", "논리", "SWITCH(값, 사례1, 결과1, …, [기본값])", "값과 같은 사례의 결과를 반환합니다."},
	{"XOR", "논리", "XOR(조건1, 조건2, …)", "참인 조건이 홀수 개면 TRUE입니다."},
	{"IFNA", "논리", "IFNA(값, 대체값)", "#N/A일 때만 대체값을 반환합니다."},
	{"ISERROR", "논리", "ISERROR(값)", "값이 오류이면 TRUE입니다."},
	{"ISERR", "논리", "ISERR(값)", "#N/A를 뺀 오류이면 TRUE입니다."},
	{"ISNA", "논리", "ISNA(값)", "값이 #N/A이면 TRUE입니다."},
	{"ISBLANK", "논리", "ISBLANK(값)", "셀이 비어 있으면 TRUE입니다."},
	{"ISBIZNO", "논리", "ISBIZNO(값)", "사업자등록번호의 검사 숫자가 맞으면 TRUE입니다. 하이픈은 있어도 됩니다."},
	{"ISCORPNO", "논리", "ISCORPNO(값)", "법인등록번호의 검사 숫자가 맞으면 TRUE입니다."},
	{"ISNUMBER", "논리", "ISNUMBER(값)", "값이 숫자이면 TRUE입니다."},
	{"ERROR.TYPE", "논리", "ERROR.TYPE(값)", "오류의 종류를 번호로 알려줍니다. #N/A는 7입니다."},
	{"ISTEXT", "논리", "ISTEXT(값)", "값이 텍스트이면 TRUE입니다."},
	{"ISNONTEXT", "논리", "ISNONTEXT(값)", "값이 텍스트가 아니면 TRUE입니다."},
	{"ISLOGICAL", "논리", "ISLOGICAL(값)", "값이 TRUE/FALSE이면 TRUE입니다."},
	{"ISEVEN", "논리", "ISEVEN(숫자)", "짝수이면 TRUE입니다."},
	{"ISODD", "논리", "ISODD(숫자)", "홀수이면 TRUE입니다."},
	{"ISEMAIL", "논리", "ISEMAIL(값)", "이메일 주소 형식이면 TRUE입니다."},
	{"ISURL", "논리", "ISURL(값)", "http로 시작하는 주소이면 TRUE입니다."},
	{"ISDATE", "논리", "ISDATE(값)", "날짜로 읽을 수 있으면 TRUE입니다."},
	{"NA", "논리", "NA()", "#N/A 오류를 만듭니다."},
	{"N", "논리", "N(값)", "값을 숫자로 바꿉니다. 텍스트는 0입니다."},
	{"TYPE", "논리", "TYPE(값)", "값의 종류를 숫자로 반환합니다."},
	{"TEXT", "텍스트", "TEXT(값, 서식)", "숫자나 날짜를 지정한 서식의 텍스트로 만듭니다."},
	{"TO_TEXT", "텍스트", "TO_TEXT(값)", "값을 텍스트로 바꿉니다."},
	{"REPLACE", "텍스트", "REPLACE(텍스트, 시작 위치, 개수, 새 텍스트)", "위치를 지정해 일부를 바꿉니다."},
	{"EXACT", "텍스트", "EXACT(텍스트1, 텍스트2)", "대소문자까지 같은지 비교합니다."},
	{"SPLIT", "텍스트", "SPLIT(텍스트, 구분자, [각 문자 구분], [빈 값 제거])", "텍스트를 나눠 여러 셀로 펼칩니다."},
	{"TEXTSPLIT", "텍스트", "TEXTSPLIT(텍스트, 열 구분자, [행 구분자], [빈 값 무시], [대소문자 구분 안 함], [채울 값])", "텍스트를 열과 행으로 나눠 표로 펼칩니다."},
	{"TEXTBEFORE", "텍스트", "TEXTBEFORE(텍스트, 구분자, [번째], [대소문자 구분 안 함], [끝을 구분자로], [없을 때])", "구분자 앞의 텍스트를 반환합니다."},
	{"TEXTAFTER", "텍스트", "TEXTAFTER(텍스트, 구분자, [번째], [대소문자 구분 안 함], [끝을 구분자로], [없을 때])", "구분자 뒤의 텍스트를 반환합니다."},
	{"JOIN", "텍스트", "JOIN(구분자, 값1, …)", "값을 구분자로 이어 붙입니다."},
	{"CHAR", "텍스트", "CHAR(코드)", "문자 코드에 해당하는 문자를 반환합니다."},
	{"CODE", "텍스트", "CODE(텍스트)", "첫 글자의 문자 코드를 반환합니다."},
	{"UNICHAR", "텍스트", "UNICHAR(코드)", "유니코드 번호에 해당하는 문자를 반환합니다."},
	{"UNICODE", "텍스트", "UNICODE(텍스트)", "첫 글자의 유니코드 번호를 반환합니다."},
	{"T", "텍스트", "T(값)", "값이 텍스트면 그대로, 아니면 빈 텍스트를 반환합니다."},
	{"CLEAN", "텍스트", "CLEAN(텍스트)", "인쇄할 수 없는 문자를 제거합니다."},
	{"DOLLAR", "텍스트", "DOLLAR(숫자, [자릿수])", "통화 형식 텍스트로 만듭니다."},
	{"FIXED", "텍스트", "FIXED(숫자, [자릿수], [구분 기호 생략])", "자릿수를 고정한 텍스트로 만듭니다."},
	{"REGEXMATCH", "텍스트", "REGEXMATCH(텍스트, 정규식)", "정규식과 일치하면 TRUE입니다."},
	{"REGEXEXTRACT", "텍스트", "REGEXEXTRACT(텍스트, 정규식)", "정규식과 일치하는 부분을 꺼냅니다."},
	{"REGEXREPLACE", "텍스트", "REGEXREPLACE(텍스트, 정규식, 바꿀 텍스트)", "정규식과 일치하는 부분을 바꿉니다."},
	{"EDATE", "날짜", "EDATE(날짜, 개월 수)", "몇 개월 뒤(앞) 같은 날짜를 반환합니다."},
	{"EOMONTH", "날짜", "EOMONTH(날짜, 개월 수)", "몇 개월 뒤(앞) 달의 말일을 반환합니다."},
	{"DAYS", "날짜", "DAYS(끝 날짜, 시작 날짜)", "두 날짜 사이의 일수를 구합니다."},
	{"DAYS360", "날짜", "DAYS360(시작 날짜, 끝 날짜)", "1년 360일 기준 일수를 구합니다."},
	{"DATEDIF", "날짜", "DATEDIF(시작 날짜, 끝 날짜, 단위)", "Y, M, D, MD, YM, YD 단위로 차이를 구합니다."},
	{"YEARFRAC", "날짜", "YEARFRAC(시작 날짜, 끝 날짜, [기준])", "기간을 연 단위 소수로 구합니다."},
	{"NETWORKDAYS", "날짜", "NETWORKDAYS(시작 날짜, 끝 날짜, [휴일])", "주말과 휴일을 뺀 근무일수를 구합니다."},
	{"WORKDAY", "날짜", "WORKDAY(시작 날짜, 근무일수, [휴일])", "근무일 기준으로 며칠 뒤 날짜를 구합니다."},
	{"WEEKNUM", "날짜", "WEEKNUM(날짜)", "연중 주 번호를 구합니다."},
	{"ISOWEEKNUM", "날짜", "ISOWEEKNUM(날짜)", "ISO 기준 주 번호를 구합니다."},
	{"TIME", "날짜", "TIME(시, 분, 초)", "시각을 하루의 분수로 돌려줍니다. 칸에 시각으로 보이려면 시각 서식을 지정하세요."},
	{"HOUR", "날짜", "HOUR(시각)", "시각에서 시를 꺼냅니다."},
	{"MINUTE", "날짜", "MINUTE(시각)", "시각에서 분을 꺼냅니다."},
	{"SECOND", "날짜", "SECOND(시각)", "시각에서 초를 꺼냅니다."},
	{"DATEVALUE", "날짜", "DATEVALUE(텍스트)", "텍스트를 날짜로 바꿉니다."},
	{"TIMEVALUE", "날짜", "TIMEVALUE(텍스트)", "시각을 하루에 대한 비율로 바꿉니다."},
	{"XLOOKUP", "조회", "XLOOKUP(찾을 값, 찾을 범위, 반환 범위, [없을 때], [일치 방식], [검색 방향])", "왼쪽·오른쪽 어디든 찾아 반환합니다. 없을 때 값을 지정할 수 있습니다."},
	{"XMATCH", "조회", "XMATCH(찾을 값, 범위, [일치 방식], [검색 방향])", "값의 위치를 유연한 방식으로 찾습니다."},
	{"LOOKUP", "조회", "LOOKUP(찾을 값, 찾을 범위, [반환 범위])", "정렬된 범위에서 값을 찾습니다."},
	{"OFFSET", "조회", "OFFSET(기준, 행 이동, 열 이동, [높이], [너비])", "기준에서 떨어진 범위를 참조합니다."},
	{"INDIRECT", "조회", "INDIRECT(주소 텍스트)", "텍스트로 쓴 주소를 참조로 바꿉니다."},
	{"IMPORTRANGE", "조회", "IMPORTRANGE(\"워크북 주소\", \"시트!범위\")", "다른 워크북의 범위를 가져옵니다. 이 워크북 소유자에게 원본 읽기 권한이 있어야 합니다."},
	{"ROW", "조회", "ROW([셀])", "행 번호를 반환합니다."},
	{"COLUMN", "조회", "COLUMN([셀])", "열 번호를 반환합니다."},
	{"ROWS", "조회", "ROWS(범위)", "범위의 행 개수를 반환합니다."},
	{"COLUMNS", "조회", "COLUMNS(범위)", "범위의 열 개수를 반환합니다."},
	{"ADDRESS", "조회", "ADDRESS(행, 열, [참조 유형], [시트 이름])", "행과 열로 주소 텍스트를 만듭니다."},
	{"CHOOSE", "조회", "CHOOSE(번호, 값1, 값2, …)", "번호에 해당하는 값을 고릅니다."},
	{"UNIQUE", "배열", "UNIQUE(범위)", "중복을 없앤 목록을 반환합니다."},
	{"SEQUENCE", "배열", "SEQUENCE(행 수, [열 수], [시작], [증가])", "연속된 숫자 배열을 만듭니다."},
	{"VSTACK", "배열", "VSTACK(배열1, 배열2, …)", "배열을 위아래로 이어 붙입니다."},
	{"HSTACK", "배열", "HSTACK(배열1, 배열2, …)", "배열을 좌우로 이어 붙입니다."},
	{"TAKE", "배열", "TAKE(배열, 행 수, [열 수])", "배열의 앞이나 뒤에서 지정한 만큼만 남깁니다."},
	{"DROP", "배열", "DROP(배열, 행 수, [열 수])", "배열의 앞이나 뒤에서 지정한 만큼을 덜어냅니다."},
	{"CHOOSEROWS", "배열", "CHOOSEROWS(배열, 행1, 행2, …)", "고른 행만 순서대로 반환합니다."},
	{"CHOOSECOLS", "배열", "CHOOSECOLS(배열, 열1, 열2, …)", "고른 열만 순서대로 반환합니다."},
	{"SORTBY", "배열", "SORTBY(배열, 기준1, [순서1], …)", "다른 배열의 값을 기준으로 정렬합니다."},
	{"LET", "배열", "LET(이름1, 값1, …, 계산식)", "수식 안에서 이름을 정해 두고 계산에 씁니다."},
	{"LAMBDA", "배열", "LAMBDA(인수1, …, 계산식)", "수식으로 함수를 만듭니다. MAP·REDUCE 등에 넘기거나 바로 호출합니다."},
	{"MAP", "배열", "MAP(배열1, …, LAMBDA)", "배열의 값마다 함수를 적용한 결과를 반환합니다."},
	{"BYROW", "배열", "BYROW(배열, LAMBDA)", "행마다 함수를 적용해 한 열로 반환합니다."},
	{"BYCOL", "배열", "BYCOL(배열, LAMBDA)", "열마다 함수를 적용해 한 행으로 반환합니다."},
	{"REDUCE", "배열", "REDUCE(초깃값, 배열, LAMBDA)", "배열을 훑으며 값을 하나로 접습니다."},
	{"SCAN", "배열", "SCAN(초깃값, 배열, LAMBDA)", "REDUCE의 중간 결과를 모두 반환합니다."},
	{"ISOMITTED", "정보", "ISOMITTED(인수)", "LAMBDA의 인수가 생략되었는지 확인합니다."},
	{"TRANSPOSE", "배열", "TRANSPOSE(범위)", "행과 열을 바꿉니다."},
	{"FLATTEN", "배열", "FLATTEN(범위1, …)", "여러 범위를 한 열로 펼칩니다."},
	{"TOCOL", "배열", "TOCOL(범위1, …)", "범위를 한 열로 펼칩니다."},
	{"TOROW", "배열", "TOROW(범위1, …)", "범위를 한 행으로 펼칩니다."},
	{"WRAPROWS", "배열", "WRAPROWS(줄, 한 줄 길이, [채울 값])", "한 줄을 정한 길이마다 접어 여러 행으로 만듭니다."},
	{"WRAPCOLS", "배열", "WRAPCOLS(줄, 한 줄 길이, [채울 값])", "한 줄을 정한 길이마다 접어 여러 열로 만듭니다."},
	{"EXPAND", "배열", "EXPAND(배열, 행 수, [열 수], [채울 값])", "배열을 정한 크기까지 넓히고 빈 자리를 채웁니다."},
	{"ARRAY_CONSTRAIN", "배열", "ARRAY_CONSTRAIN(범위, 행 수, 열 수)", "배열을 지정한 크기로 자릅니다."},
	{"ARRAYFORMULA", "배열", "ARRAYFORMULA(수식)", "범위 전체에 수식을 한 번에 적용합니다."},
	{"QUERY", "배열", "QUERY(범위, 질의, [머리글 행 수])", "select·where·group by·order by로 표를 조회합니다. 열은 범위 안 순서대로 A, B, C…입니다."},
	{"HANGULNUM", "텍스트", "HANGULNUM(숫자)", "수를 한글로 적습니다. 3200000 → 삼백이십만."},
	{"HANGULWON", "텍스트", "HANGULWON(숫자)", "금액을 문서에 적는 꼴로 바꿉니다. 3200000 → 일금 삼백이십만원정."},
	{"FORMATBIZNO", "텍스트", "FORMATBIZNO(값)", "사업자등록번호를 123-45-67890 꼴로 적습니다."},
	{"MASKRRN", "텍스트", "MASKRRN(값, [남길 자리])", "주민등록번호 뒷자리를 가립니다. 기본은 한 자리만 남깁니다."},
	{"KOREANHOLIDAYS", "날짜", "KOREANHOLIDAYS(연도)", "그 해 한국 공휴일을 대체공휴일까지 날짜 배열로 돌려줍니다. NETWORKDAYS 나 WORKDAY 의 휴일 인자에 그대로 씁니다. 2020~2030년."},
	{"KOREANHOLIDAYNAME", "날짜", "KOREANHOLIDAYNAME(날짜)", "그 날이 한국 공휴일이면 이름을, 아니면 빈 글자를 돌려줍니다."},
	{"WEBSERVICE", "웹", "WEBSERVICE(\"https://주소\")", "허용된 주소에서 본문을 글자로 가져옵니다. 관리자가 외부 호출을 켜고 허용 호스트에 적은 곳만 됩니다."},
	{"IMPORTDATA", "웹", "IMPORTDATA(\"https://주소\")", "허용된 주소의 CSV 를 표로 가져옵니다. 관리자가 외부 호출을 켜고 허용 호스트에 적은 곳만 됩니다."},
	{"SPARKLINE", "배열", "SPARKLINE(범위, [옵션])", "셀 안에 선·막대·승패 미니 차트를 그립니다."},
}

// Catalog lists every function the evaluator understands, in menu order.
func Catalog() []FunctionDoc { return append([]FunctionDoc(nil), catalog...) }

// callableNames 는 카탈로그에 적힌 함수 이름들이다. 괄호 없이 적힌 이름이
// 함수를 가리키는지 가리는 데 쓴다. 카탈로그는 구현된 함수를 모두 담도록
// 시험이 붙잡고 있으므로 따로 목록을 만들지 않는다.
var callableNames = func() map[string]struct{} {
	names := make(map[string]struct{}, len(catalog))
	for _, entry := range catalog {
		names[strings.ToUpper(entry.Name)] = struct{}{}
	}
	return names
}()

// isCallableName 은 이 이름이 부를 수 있는 것인지 본다. 워크북에 저장해 둔
// 이름 있는 수식도 함께 본다 — 사람이 만든 함수라고 다를 이유가 없다.
func isCallableName(name string, scope Scope) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if _, found := callableNames[upper]; found {
		return true
	}
	_, named := scope.NamedFunctions[upper]
	return named
}

var now = time.Now

// evaluateLibrary handles the functions whose arguments are ordinary flattened
// values. It reports false when the name belongs to no known function so the
// caller can raise #NAME? once, in one place.
func evaluateLibrary(name string, values []any) (any, bool, error) {
	switch name {
	case "COUNTA":
		count := 0
		for _, value := range values {
			if value != nil && display(value) != "" {
				count++
			}
		}
		return float64(count), true, nil
	case "COUNTBLANK":
		count := 0
		for _, value := range values {
			if value == nil || display(value) == "" {
				count++
			}
		}
		return float64(count), true, nil
	case "MEDIAN":
		numbers := numericValues(values)
		if len(numbers) == 0 {
			return nil, true, formulaError("#NUM!", "MEDIAN requires at least one number")
		}
		sorted := append([]float64(nil), numbers...)
		for index := 1; index < len(sorted); index++ {
			for position := index; position > 0 && sorted[position] < sorted[position-1]; position-- {
				sorted[position], sorted[position-1] = sorted[position-1], sorted[position]
			}
		}
		middle := len(sorted) / 2
		if len(sorted)%2 == 1 {
			return sorted[middle], true, nil
		}
		return (sorted[middle-1] + sorted[middle]) / 2, true, nil
	case "PRODUCT":
		numbers := numericValues(values)
		if len(numbers) == 0 {
			return float64(0), true, nil
		}
		result := 1.0
		for _, number := range numbers {
			result *= number
		}
		return result, true, nil
	case "ROUNDUP", "ROUNDDOWN":
		number, digits, err := roundingArguments(name, values)
		if err != nil {
			return nil, true, err
		}
		// ROUND 와 같은 십진 셈을 쓴다. 세 함수가 서로 다른 값을 내면 안 된다.
		if name == "ROUNDUP" {
			return decimalRound(number, decimalPlaces(digits), roundAwayFromZero), true, nil
		}
		return decimalRound(number, decimalPlaces(digits), roundTowardZero), true, nil
	case "ABS", "INT", "SQRT":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a number")
		}
		switch name {
		case "ABS":
			return math.Abs(number), true, nil
		case "INT":
			return math.Floor(number), true, nil
		}
		if number < 0 {
			return nil, true, formulaError("#NUM!", "SQRT requires a value of zero or more")
		}
		return math.Sqrt(number), true, nil
	case "MOD", "POWER":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		left, leftOK := toNumber(values[0])
		right, rightOK := toNumber(values[1])
		if !leftOK || !rightOK {
			return nil, true, formulaError("#VALUE!", name+" requires numbers")
		}
		if name == "MOD" {
			if right == 0 {
				return nil, true, formulaError("#DIV/0!", "MOD cannot divide by zero")
			}
			return math.Mod(math.Mod(left, right)+right, right), true, nil
		}
		return math.Pow(left, right), true, nil
	case "LEN":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		return float64(len([]rune(display(values[0])))), true, nil
	case "TRIM":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		return strings.Join(strings.Fields(display(values[0])), " "), true, nil
	case "UPPER", "LOWER", "PROPER":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		text := display(values[0])
		switch name {
		case "UPPER":
			return strings.ToUpper(text), true, nil
		case "LOWER":
			return strings.ToLower(text), true, nil
		}
		return properCase(text), true, nil
	case "SUBSTITUTE":
		if len(values) != 3 && len(values) != 4 {
			return nil, true, argError(name)
		}
		text, old, replacement := display(values[0]), display(values[1]), display(values[2])
		if len(values) == 3 {
			return strings.ReplaceAll(text, old, replacement), true, nil
		}
		// 네 번째 인수는 "몇 번째로 나온 것만 바꿀지" 이다.
		instance, ok := toNumber(values[3])
		if !ok || instance < 1 || instance != math.Trunc(instance) {
			return nil, true, formulaError("#VALUE!", "SUBSTITUTE instance must be a whole number of at least one")
		}
		if old == "" {
			return text, true, nil
		}
		// 그만큼 나오지 않으면 원래 글이 그대로 나온다. 엑셀도 그렇게 한다.
		searched, remaining := 0, int(instance)
		for {
			found := strings.Index(text[searched:], old)
			if found < 0 {
				return text, true, nil
			}
			searched += found
			remaining--
			if remaining == 0 {
				return text[:searched] + replacement + text[searched+len(old):], true, nil
			}
			searched += len(old)
		}
	case "FIND", "SEARCH":
		if len(values) != 2 && len(values) != 3 {
			return nil, true, argError(name)
		}
		needle, haystack := display(values[0]), display(values[1])
		if name == "SEARCH" {
			needle, haystack = strings.ToLower(needle), strings.ToLower(haystack)
		}
		// 세 번째 인수는 몇 번째 글자부터 찾을지다. 두 번째로 나온 것을
		// 찾을 때 쓴다. 돌려주는 자리는 글 전체에서 센 자리다.
		letters := []rune(haystack)
		start := 0
		if len(values) == 3 {
			number, ok := toNumber(values[2])
			if !ok || number < 1 || number != math.Trunc(number) {
				return nil, true, formulaError("#VALUE!", name+" start position must be a whole number of at least one")
			}
			start = int(number) - 1
			if start > len(letters) {
				return nil, true, formulaError("#VALUE!", name+" start position is past the end of the text")
			}
		}
		offset := len(string(letters[:start]))
		index := strings.Index(haystack[offset:], needle)
		if index < 0 {
			return nil, true, formulaError("#VALUE!", name+" did not find the text")
		}
		return float64(len([]rune(haystack[:offset+index])) + 1), true, nil
	case "REPT":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		count, ok := toNumber(values[1])
		if !ok || count < 0 || count > 10_000 {
			return nil, true, formulaError("#VALUE!", "REPT requires a repeat count between 0 and 10000")
		}
		return strings.Repeat(display(values[0]), int(count)), true, nil
	case "TEXTJOIN":
		if len(values) < 3 {
			return nil, true, argError(name)
		}
		separator, skipEmpty := display(values[0]), truthy(values[1])
		parts := make([]string, 0, len(values)-2)
		for _, value := range values[2:] {
			text := display(value)
			if skipEmpty && text == "" {
				continue
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, separator), true, nil
	case "VALUE":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", "VALUE requires a number in text form")
		}
		return number, true, nil
	case "HYPERLINK":
		if len(values) < 1 || len(values) > 2 {
			return nil, true, argError(name)
		}
		if len(values) == 2 && display(values[1]) != "" {
			return display(values[1]), true, nil
		}
		return display(values[0]), true, nil
	case "TODAY":
		if len(values) != 0 {
			return nil, true, argError(name)
		}
		return now().Format("2006-01-02"), true, nil
	case "NOW":
		if len(values) != 0 {
			return nil, true, argError(name)
		}
		return now().Format("2006-01-02 15:04:05"), true, nil
	case "YEAR", "MONTH", "DAY", "WEEKDAY":
		// WEEKDAY 만 두 번째 인수로 한 주가 어느 요일에 시작하는지 고를 수
		// 있다. 나머지는 날짜 하나만 받는다.
		if len(values) != 1 && !(name == "WEEKDAY" && len(values) == 2) {
			return nil, true, argError(name)
		}
		moment, ok := parseDate(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a date")
		}
		switch name {
		case "YEAR":
			return float64(moment.Year()), true, nil
		case "MONTH":
			return float64(int(moment.Month())), true, nil
		case "DAY":
			return float64(moment.Day()), true, nil
		}
		weekdayType := 1.0
		if len(values) == 2 {
			number, ok := toNumber(values[1])
			if !ok {
				return nil, true, formulaError("#VALUE!", "WEEKDAY requires a number for the week type")
			}
			weekdayType = number
		}
		return weekdayNumber(moment, int(weekdayType))
	}
	return nil, false, nil
}

// weekdayNumber 는 엑셀과 시트가 쓰는 요일 번호 방식을 그대로 따른다.
// 어느 요일을 한 주의 처음으로 볼지, 번호를 1 부터 셀지 0 부터 셀지가
// 나라와 쓰임에 따라 다르기 때문에 방식이 여럿이다.
//
//	1(생략) 일요일이 1        2  월요일이 1        3  월요일이 0
//	11~17   월~일 차례로 그 요일이 1
func weekdayNumber(moment time.Time, weekdayType int) (any, bool, error) {
	// time.Weekday 는 일요일이 0 이다.
	sunday := int(moment.Weekday())
	switch weekdayType {
	case 1:
		return float64(sunday + 1), true, nil
	case 2:
		return float64((sunday+6)%7 + 1), true, nil
	case 3:
		return float64((sunday + 6) % 7), true, nil
	}
	// 11 은 월요일, 12 는 화요일… 17 은 일요일을 한 주의 처음으로 본다.
	if weekdayType >= 11 && weekdayType <= 17 {
		first := (weekdayType - 11 + 1) % 7
		return float64((sunday-first+7)%7 + 1), true, nil
	}
	return nil, true, formulaError("#NUM!", "WEEKDAY week type must be 1, 2, 3 or 11 to 17")
}

func sign(number float64) float64 {
	if number < 0 {
		return -1
	}
	return 1
}

func roundingArguments(name string, values []any) (float64, float64, error) {
	if len(values) < 1 || len(values) > 2 {
		return 0, 0, argError(name)
	}
	number, ok := toNumber(values[0])
	if !ok {
		return 0, 0, formulaError("#VALUE!", name+" requires a number")
	}
	digits := 0.0
	if len(values) == 2 && !omitted(values[1]) {
		digits, _ = toNumber(values[1])
	}
	return number, digits, nil
}

// properCase 는 글자가 아닌 것 뒤에 오는 글자를 모두 큰 글자로 적는다.
//
// 예전에는 빈칸·탭·붙임표·밑줄 뒤만 낱말의 처음으로 보았다. 그래서
// PROPER("o'neil") 이 "O'neil", PROPER("76budget") 이 "76budget" 이었다.
// 엑셀은 따옴표든 숫자든 글자가 아니면 그 뒤를 낱말의 처음으로 본다:
// "O'Neil", "76Budget". 사람이 이름과 주소를 다듬을 때 쓰는 함수라,
// 답이 다르면 엑셀에서 옮겨 온 표가 조용히 어긋난다.
func properCase(text string) string {
	var builder strings.Builder
	start := true
	for _, letter := range text {
		if start {
			builder.WriteString(strings.ToUpper(string(letter)))
		} else {
			builder.WriteString(strings.ToLower(string(letter)))
		}
		start = !unicode.IsLetter(letter)
	}
	return builder.String()
}

// Dates are stored as the text DATE produces, so both the plain date and the
// date with a time component are accepted.
// parseDate 는 날짜를 두 가지 모습으로 받는다.
//
//   - 사람이 적은 글: "2024-01-15"
//   - 엑셀에서 가져온 날 수: 45306
//
// 엑셀 파일을 읽어 오면 날짜가 일련번호로 담긴다. 브라우저의 격자는
// web/src/lib/cellFormat.ts 에서 이 번호를 날짜로 읽어 제대로 보여주고
// 있었지만, 여기서는 읽지 못해 YEAR, MONTH, WEEKDAY, DATEDIF, TEXT 이
// 모두 #VALUE! 를 냈다. 가져온 파일의 날짜 칸은 보이기만 하고 셈에는
// 쓸 수 없었다.
func parseDate(value any) (time.Time, bool) {
	// 글로 적힌 "2024" 를 날 수로 보면 안 되므로 진짜 숫자만 가른다.
	switch number := value.(type) {
	case float64:
		return serialDate(number)
	case int:
		return serialDate(float64(number))
	}
	text := strings.TrimSpace(display(value))
	if text == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04:05", time.RFC3339, "2006/01/02"} {
		if moment, err := time.Parse(layout, text); err == nil {
			return moment, true
		}
	}
	return time.Time{}, false
}

// serialDate 는 표 프로그램이 쓰는 날 수를 날짜로 바꾼다.
//
// 엑셀은 1900 년을 윤년으로 잘못 센다. 그래서 일련번호 60 은 없는 날인
// 1900-02-29 를 가리킨다. 60 보다 작은 번호는 하루 뒤에서 세기 시작해야
// 1900-01-01 이 1 번이 된다.
//
// web/src/lib/cellFormat.ts 의 spreadsheetDate 가 **같은 셈** 을 한다.
// 격자에 보이는 날짜와 수식이 읽는 날짜가 어긋나면 안 된다.
// SerialDate 는 표 프로그램이 쓰는 날 수를 날짜로 바꾼다. 검증 규칙도
// 같은 셈을 써야 하므로 밖에서 부를 수 있게 열어 둔다. 따로 셈하면
// 격자에 보이는 날짜와 검증이 읽는 날짜가 어긋난다.
func SerialDate(serial float64) (time.Time, bool) { return serialDate(serial) }

func serialDate(serial float64) (time.Time, bool) {
	// 9999-12-31 이 2958465 다. 그 너머는 날짜가 아니다.
	if serial < 0 || serial > 2958465 || math.IsNaN(serial) {
		return time.Time{}, false
	}
	epoch := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	if serial < 60 {
		epoch = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
	}
	days := math.Floor(serial)
	// 하루 안의 자리는 초 단위로 반올림한다. 그냥 곱해서 자르면 0.675 일이
	// 16시간 11분 59.999초가 되어 16:11 로 보인다. 16:12 여야 한다.
	seconds := math.Round((serial - days) * 24 * 60 * 60)
	return epoch.AddDate(0, 0, int(days)).Add(time.Duration(seconds) * time.Second), true
}

// DateSerial 은 칸의 값을 표 프로그램이 쓰는 날 수로 바꾼다. 숫자면 그대로
// 날 수로 보고, "2026-01-05" 같은 글자면 읽어서 셈한다.
//
// DATE() 는 글자를 내므로 날짜를 담은 칸은 대개 글자다. 그림을 그리는 쪽은
// 두 수 사이에 막대를 그어야 하니 수가 필요하다. 셈하는 자리를 여기 한 곳에
// 두는 까닭은 serialDate 와 짝이 어긋나면 안 되기 때문이다.
func DateSerial(value any) (float64, bool) {
	moment, ok := parseDate(value)
	if !ok {
		return 0, false
	}
	day := time.Date(moment.Year(), moment.Month(), moment.Day(), 0, 0, 0, 0, time.UTC)
	seconds := float64(moment.Hour()*3600 + moment.Minute()*60 + moment.Second())
	serial := day.Sub(time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)).Hours() / 24
	// 1900 년을 윤년으로 잘못 센 자리. serialDate 가 60 보다 작은 번호를
	// 하루 뒤에서 세므로 되돌릴 때도 그렇게 해야 짝이 맞는다.
	if serial < 60 {
		serial = day.Sub(time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)).Hours() / 24
	}
	if serial < 0 {
		return 0, false
	}
	return serial + seconds/86400, true
}
