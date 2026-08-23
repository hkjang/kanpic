package formula

import (
	"math"
	"strings"
	"time"
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
	{"ARRAY_CONSTRAIN", "배열", "ARRAY_CONSTRAIN(범위, 행 수, 열 수)", "배열을 지정한 크기로 자릅니다."},
	{"ARRAYFORMULA", "배열", "ARRAYFORMULA(수식)", "범위 전체에 수식을 한 번에 적용합니다."},
	{"QUERY", "배열", "QUERY(범위, 질의, [머리글 행 수])", "select·where·group by·order by로 표를 조회합니다. 열은 범위 안 순서대로 A, B, C…입니다."},
	{"SPARKLINE", "배열", "SPARKLINE(범위, [옵션])", "셀 안에 선·막대·승패 미니 차트를 그립니다."},
}

// Catalog lists every function the evaluator understands, in menu order.
func Catalog() []FunctionDoc { return append([]FunctionDoc(nil), catalog...) }

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
			return decimalRound(number, int(digits), roundAwayFromZero), true, nil
		}
		return decimalRound(number, int(digits), roundTowardZero), true, nil
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

func properCase(text string) string {
	var builder strings.Builder
	start := true
	for _, letter := range text {
		if start {
			builder.WriteString(strings.ToUpper(string(letter)))
		} else {
			builder.WriteString(strings.ToLower(string(letter)))
		}
		start = letter == ' ' || letter == '\t' || letter == '-' || letter == '_'
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
