package workbook

// Formula templates double as a working cookbook: every sheet ships with data
// so each example calculates a real answer that can be copied into other work.
// They are kept apart from the business templates because they are organised by
// function family rather than by the job being done.
var formulaTemplates = []Template{
	tmpl("formula-lookup", "조회 함수 모음", "수식·함수", "VLOOKUP·XLOOKUP·INDEX/MATCH를 같은 표에 적용해 상황별 차이를 비교합니다.",
		sheet("조회").tab("#5268a6").cols(90, 170, 110, 90, 110, 250).
			title("조회 함수 모음").note("아래 단가표를 기준으로 조회 결과가 계산됩니다. 조회 값만 바꿔 보세요.").
			head("코드", "제품", "단가", "재고", "담당", "비고").
			rows(
				row("P-101", "무선 마우스", 24000, 120, "김수현", "베스트셀러"),
				row("P-102", "기계식 키보드", 89000, 45, "박지훈", "재고 부족 주의"),
				row("P-103", "27인치 모니터", 320000, 18, "이서연", ""),
				row("P-104", "USB-C 허브", 46000, 210, "김수현", ""),
				row("P-105", "노트북 거치대", 38000, 76, "박지훈", ""),
				row("P-106", "웹캠 1080p", 62000, 33, "이서연", "신규 입고"),
			).
			format(formatMoney, 3).format(formatNumber, 4).
			summary(
				row("찾을 코드", "P-104"),
				row("VLOOKUP 단가", won("=VLOOKUP($B$11,$A${first}:$E${last},3,FALSE)"), "표의 왼쪽 첫 열에서 찾아 오른쪽 값을 가져옵니다."),
				row("XLOOKUP 담당", "=XLOOKUP($B$11,$A${first}:$A${last},$E${first}:$E${last},\"미지정\")", "찾을 범위와 반환 범위를 따로 지정하고, 없을 때 값을 정할 수 있습니다."),
				row("INDEX/MATCH 제품", "=INDEX($B${first}:$B${last},MATCH($B$11,$A${first}:$A${last},0))", "반환 열이 조회 열 왼쪽에 있어도 동작합니다."),
				row("XMATCH 행 위치", "=XMATCH($B$11,$A${first}:$A${last})", "표 안에서 몇 번째 행인지 알려 줍니다."),
				row("가격대 등급", "=IFS(VLOOKUP($B$11,$A${first}:$C${last},3,FALSE)>=300000,\"고급\",VLOOKUP($B$11,$A${first}:$C${last},3,FALSE)>=100000,\"중급\",TRUE,\"보급\")", "IFS로 가격 구간을 나눕니다."),
				row("없는 코드 처리", "=XLOOKUP(\"P-999\",$A${first}:$A${last},$B${first}:$B${last},\"코드 없음\")", "IFERROR 없이도 대체 값을 지정합니다."),
				row("재고 최다 제품", "=INDEX($B${first}:$B${last},MATCH(MAX($D${first}:$D${last}),$D${first}:$D${last},0))", "최댓값의 위치로 이름을 찾습니다."),
			),
	),
	tmpl("formula-conditional", "조건부 집계 모음", "수식·함수", "SUMIFS·COUNTIFS·AVERAGEIFS·MAXIFS로 조건에 맞는 값만 집계합니다.",
		sheet("조건집계").tab("#0f766e").cols(100, 100, 110, 120, 110, 240).
			title("조건부 집계 모음").note("지역·채널·금액 조건을 조합해 필요한 숫자만 뽑아내는 방법입니다.").
			head("일자", "지역", "채널", "매출", "수량", "담당").
			rows(
				row("2026-07-02", "서울", "온라인", 4200000, 62, "김수현"),
				row("2026-07-05", "부산", "오프라인", 1850000, 24, "박지훈"),
				row("2026-07-09", "서울", "오프라인", 3100000, 41, "이서연"),
				row("2026-07-14", "대구", "온라인", 970000, 15, "김수현"),
				row("2026-07-18", "서울", "온라인", 5600000, 88, "박지훈"),
				row("2026-07-23", "부산", "온라인", 2450000, 33, "이서연"),
				row("2026-07-27", "대구", "오프라인", 1320000, 19, "김수현"),
				row("2026-07-30", "서울", "온라인", 3800000, 55, "이서연"),
			).
			format(formatDate, 1).format(formatMoney, 4).format(formatNumber, 5).
			total("합계", nil, nil, "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", nil).
			summary(
				row("서울 매출", won("=SUMIF($B${first}:$B${last},\"서울\",$D${first}:$D${last})"), "조건 하나면 SUMIF로 충분합니다."),
				row("서울 온라인 매출", won("=SUMIFS($D${first}:$D${last},$B${first}:$B${last},\"서울\",$C${first}:$C${last},\"온라인\")"), "조건이 둘 이상이면 SUMIFS입니다."),
				row("300만 이상 건수", "=COUNTIFS($D${first}:$D${last},\">=3000000\")", "비교 연산자는 따옴표 안에 씁니다."),
				row("온라인 평균 매출", won("=AVERAGEIFS($D${first}:$D${last},$C${first}:$C${last},\"온라인\")"), "조건에 맞는 값만 평균 냅니다."),
				row("부산 최대 매출", won("=MAXIFS($D${first}:$D${last},$B${first}:$B${last},\"부산\")"), "조건 범위 안의 최댓값입니다."),
				row("서울 최소 수량", num("=MINIFS($E${first}:$E${last},$B${first}:$B${last},\"서울\")"), "MAXIFS와 짝을 이룹니다."),
				row("이름에 '김' 포함", "=COUNTIF($F${first}:$F${last},\"김*\")", "별표는 아무 글자나 대신합니다."),
				row("지역 수", "=COUNTUNIQUE($B${first}:$B${last})", "중복을 뺀 개수입니다."),
				row("가중 평균 단가", won("=IFERROR(SUMPRODUCT($D${first}:$D${last},$E${first}:$E${last})/SUM($E${first}:$E${last}),0)"), "SUMPRODUCT로 가중치를 곱해 더합니다."),
			),
	),
	tmpl("formula-text", "텍스트 정리 도구", "수식·함수", "붙여넣은 텍스트를 자르고 나누고 다시 붙여 원하는 형태로 다듬습니다.",
		sheet("텍스트").tab("#8b5cf6").cols(230, 130, 130, 140, 130, 170).
			title("텍스트 정리 도구").note("A열에 원본을 붙여 넣으면 나머지 열이 자동으로 정리됩니다.").
			head("원본", "공백 제거", "이름", "도메인", "전화 뒷자리", "표시용 이름").
			rows(
				row("  김수현 <soo.kim@corp.example> 010-1234-5678 ", "=TRIM(A{r})", "=INDEX(SPLIT(B{r},\" \"),1,1)", "=IFERROR(REGEXEXTRACT(A{r},\"@([A-Za-z0-9.-]+)\"),\"\")", "=IFERROR(REGEXEXTRACT(A{r},\"[0-9][0-9][0-9][0-9]$\"),RIGHT(TRIM(A{r}),4))", "=B{r}&\" (\"&D{r}&\")\""),
				row(" 박지훈 <ji.park@corp.example> 010-2222-3333 ", "=TRIM(A{r})", "=INDEX(SPLIT(B{r},\" \"),1,1)", "=IFERROR(REGEXEXTRACT(A{r},\"@([A-Za-z0-9.-]+)\"),\"\")", "=IFERROR(REGEXEXTRACT(A{r},\"[0-9][0-9][0-9][0-9]$\"),RIGHT(TRIM(A{r}),4))", "=B{r}&\" (\"&D{r}&\")\""),
				row("이서연 <seo.lee@partner.example> 010-8888-9999", "=TRIM(A{r})", "=INDEX(SPLIT(B{r},\" \"),1,1)", "=IFERROR(REGEXEXTRACT(A{r},\"@([A-Za-z0-9.-]+)\"),\"\")", "=IFERROR(REGEXEXTRACT(A{r},\"[0-9][0-9][0-9][0-9]$\"),RIGHT(TRIM(A{r}),4))", "=B{r}&\" (\"&D{r}&\")\""),
				row("  최민준 <min.choi@corp.example> 010-4444-5555", "=TRIM(A{r})", "=INDEX(SPLIT(B{r},\" \"),1,1)", "=IFERROR(REGEXEXTRACT(A{r},\"@([A-Za-z0-9.-]+)\"),\"\")", "=IFERROR(REGEXEXTRACT(A{r},\"[0-9][0-9][0-9][0-9]$\"),RIGHT(TRIM(A{r}),4))", "=B{r}&\" (\"&D{r}&\")\""),
			).
			summary(
				row("사내 주소 수", "=COUNTIF($D${first}:$D${last},\"corp.example\")", "정리된 도메인으로 바로 셀 수 있습니다."),
				row("이름 목록", "=TEXTJOIN(\", \",TRUE,$C${first}:$C${last})", "TEXTJOIN은 빈 값을 건너뜁니다."),
				row("이메일 형식 확인", "=COUNTIF($D${first}:$D${last},\"*.*\")", "도메인에 점이 있는지 확인합니다."),
				row("숫자 서식 예시", "=TEXT(1234567.891,\"#,##0.00\")", "TEXT는 숫자를 원하는 서식의 글자로 바꿉니다."),
				row("백분율 서식 예시", "=TEXT(0.2567,\"0.0%\")", "보고서 문장에 값을 끼워 넣을 때 씁니다."),
				row("정규식 치환 예시", "=REGEXREPLACE(\"010-1234-5678\",\"[0-9][0-9][0-9][0-9]$\",\"****\")", "뒷자리를 가릴 때 쓰는 방법입니다."),
			),
	),
	tmpl("formula-date", "날짜 계산 도구", "수식·함수", "만기일·근무일·경과 기간을 계산하는 날짜 함수를 한 화면에 모았습니다.",
		sheet("날짜").tab("#c2703d").cols(120, 120, 130, 120, 120, 230).
			title("날짜 계산 도구").note("계약 시작일과 기간만 넣으면 만기일과 남은 일수가 계산됩니다.").
			head("계약", "시작일", "기간(개월)", "만기일", "정산 마감", "남은 근무일").
			rows(
				row("A사 유지보수", "2026-01-15", 12, "=EDATE(B{r},C{r})", "=EOMONTH(D{r},0)", "=NETWORKDAYS(TODAY(),D{r})"),
				row("B사 라이선스", "2026-03-01", 24, "=EDATE(B{r},C{r})", "=EOMONTH(D{r},0)", "=NETWORKDAYS(TODAY(),D{r})"),
				row("C사 컨설팅", "2026-06-10", 6, "=EDATE(B{r},C{r})", "=EOMONTH(D{r},0)", "=NETWORKDAYS(TODAY(),D{r})"),
				row("D사 클라우드", "2026-09-01", 36, "=EDATE(B{r},C{r})", "=EOMONTH(D{r},0)", "=NETWORKDAYS(TODAY(),D{r})"),
			).
			format(formatDate, 2, 4, 5).format(formatNumber, 3, 6).
			summary(
				row("가장 빠른 만기", "=MIN($D${first}:$D${last})", "날짜도 크기를 비교할 수 있습니다."),
				row("A사 경과 개월", "=DATEDIF($B${first},TODAY(),\"M\")", "DATEDIF는 Y·M·D·MD·YM·YD 단위를 지원합니다."),
				row("A사 경과 연수", "=DATEDIF($B${first},TODAY(),\"Y\")&\"년\"", "연 단위만 뽑아 문장에 붙였습니다."),
				row("이번 달 말일", "=EOMONTH(TODAY(),0)", "월말 마감일을 자동으로 잡습니다."),
				row("3영업일 뒤", "=WORKDAY(TODAY(),3)", "주말을 건너뛴 날짜입니다."),
				row("연 환산 기간", dec("=YEARFRAC($B${first},$D${first})"), "이자 계산에 쓰는 연 단위 기간입니다."),
				row("이번 주 번호", "=ISOWEEKNUM(TODAY())", "주간 보고 번호로 씁니다."),
				row("오늘 요일", "=CHOOSE(WEEKDAY(TODAY()),\"일\",\"월\",\"화\",\"수\",\"목\",\"금\",\"토\")", "WEEKDAY와 CHOOSE를 함께 씁니다."),
			),
	),
	tmpl("formula-finance", "재무 함수 계산기", "수식·함수", "대출 상환액과 투자 수익률을 PMT·IPMT·NPV·IRR로 계산합니다.",
		sheet("대출").tab("#0f766e").cols(150, 140, 130, 130, 130, 200).
			title("대출 상환 계산").note("원금·연이율·기간만 바꾸면 월 상환액과 회차별 원금·이자가 다시 계산됩니다.").
			head("회차", "상환액", "이자", "원금", "잔액", "설명").
			rows(
				row(1, "=PMT($B$10/12,$B$11,$B$9)", "=IPMT($B$10/12,A{r},$B$11,$B$9)", "=PPMT($B$10/12,A{r},$B$11,$B$9)", "=$B$9+CUMPRINC($B$10/12,$B$11,$B$9,1,A{r},0)", "첫 회차는 이자 비중이 가장 큽니다."),
				row(12, "=PMT($B$10/12,$B$11,$B$9)", "=IPMT($B$10/12,A{r},$B$11,$B$9)", "=PPMT($B$10/12,A{r},$B$11,$B$9)", "=$B$9+CUMPRINC($B$10/12,$B$11,$B$9,1,A{r},0)", "1년 뒤 잔액입니다."),
				row(60, "=PMT($B$10/12,$B$11,$B$9)", "=IPMT($B$10/12,A{r},$B$11,$B$9)", "=PPMT($B$10/12,A{r},$B$11,$B$9)", "=$B$9+CUMPRINC($B$10/12,$B$11,$B$9,1,A{r},0)", "5년 뒤 잔액입니다."),
				row(120, "=PMT($B$10/12,$B$11,$B$9)", "=IPMT($B$10/12,A{r},$B$11,$B$9)", "=PPMT($B$10/12,A{r},$B$11,$B$9)", "=$B$9+CUMPRINC($B$10/12,$B$11,$B$9,1,A{r},0)", "10년 뒤 잔액입니다."),
			).
			format(formatNumber, 1).format(formatMoney, 2, 3, 4, 5).
			summary(
				row("대출 원금", won(300000000)),
				row("연이율", pct(0.045)),
				row("총 회차(개월)", num(360)),
				row("월 상환액", won("=PMT($B$10/12,$B$11,$B$9)")),
				row("총 상환액", won("=PMT($B$10/12,$B$11,$B$9)*$B$11")),
				row("총 이자", won("=CUMIPMT($B$10/12,$B$11,$B$9,1,$B$11,0)")),
				row("1년 차 이자", won("=CUMIPMT($B$10/12,$B$11,$B$9,1,12,0)")),
				row("실효 연이율", pct("=EFFECT($B$10,12)")),
			),
		sheet("투자").tab("#5268a6").cols(120, 150, 150, 130, 130, 220).
			title("투자안 평가").note("현금흐름과 날짜를 넣으면 NPV·IRR·XIRR로 투자 가치를 비교합니다.").
			head("시점", "날짜", "현금흐름", "할인 계수", "현재가치", "설명").
			rows(
				row(0, "2026-01-01", -50000000, 1, "=C{r}*D{r}", "초기 투자입니다."),
				row(1, "2026-12-31", 14000000, "=1/POWER(1+$B$10,A{r})", "=C{r}*D{r}", "1년 차 회수액입니다."),
				row(2, "2027-12-31", 18000000, "=1/POWER(1+$B$10,A{r})", "=C{r}*D{r}", "2년 차 회수액입니다."),
				row(3, "2028-12-31", 21000000, "=1/POWER(1+$B$10,A{r})", "=C{r}*D{r}", "3년 차 회수액입니다."),
				row(4, "2029-12-31", 23000000, "=1/POWER(1+$B$10,A{r})", "=C{r}*D{r}", "4년 차 회수액입니다."),
			).
			format(formatDate, 2).format(formatMoney, 3, 5).format(formatDecimal, 4).
			summary(
				row("할인율", pct(0.08)),
				row("순현재가치(NPV)", won("=C{first}+NPV($B$10,C5:C{last})")),
				row("현재가치 합계", won("=SUM($E${first}:$E${last})")),
				row("내부수익률(IRR)", pct("=IRR($C${first}:$C${last})")),
				row("날짜 기준 XIRR", pct("=XIRR($C${first}:$C${last},$B${first}:$B${last})")),
				row("XNPV", won("=XNPV($B$10,$C${first}:$C${last},$B${first}:$B${last})")),
				row("회수 기간(년)", dec("=IFERROR(PDURATION($B$10,-C{first},SUM(C5:C{last})),0)")),
			),
	),
	tmpl("formula-statistics", "통계 분석 기초", "수식·함수", "평균과 표준편차, 순위와 상관관계까지 데이터를 통계로 요약합니다.",
		sheet("통계").tab("#8b5cf6").cols(110, 110, 110, 110, 110, 230).
			title("통계 분석 기초").note("광고비와 매출의 관계를 보고 다음 달 매출을 예측합니다.").
			head("월", "광고비", "매출", "이익률", "순위", "설명").
			rows(
				row("2026-01", 3200000, 41000000, "=IFERROR((C{r}-B{r}*4)/C{r},0)", "=RANK(C{r},$C${first}:$C${last})", ""),
				row("2026-02", 2800000, 37500000, "=IFERROR((C{r}-B{r}*4)/C{r},0)", "=RANK(C{r},$C${first}:$C${last})", ""),
				row("2026-03", 4100000, 49800000, "=IFERROR((C{r}-B{r}*4)/C{r},0)", "=RANK(C{r},$C${first}:$C${last})", ""),
				row("2026-04", 3600000, 45200000, "=IFERROR((C{r}-B{r}*4)/C{r},0)", "=RANK(C{r},$C${first}:$C${last})", ""),
				row("2026-05", 5200000, 61400000, "=IFERROR((C{r}-B{r}*4)/C{r},0)", "=RANK(C{r},$C${first}:$C${last})", ""),
				row("2026-06", 4800000, 57300000, "=IFERROR((C{r}-B{r}*4)/C{r},0)", "=RANK(C{r},$C${first}:$C${last})", ""),
				row("2026-07", 3900000, 46900000, "=IFERROR((C{r}-B{r}*4)/C{r},0)", "=RANK(C{r},$C${first}:$C${last})", ""),
			).
			format(formatMoney, 2, 3).format(formatPercent, 4).format(formatNumber, 5).
			summary(
				row("평균 매출", won("=AVERAGE($C${first}:$C${last})")),
				row("중앙값", won("=MEDIAN($C${first}:$C${last})")),
				row("표준편차", won("=STDEV($C${first}:$C${last})"), "표본 표준편차입니다. 모집단이면 STDEVP를 씁니다."),
				row("변동계수", pct("=IFERROR(STDEV($C${first}:$C${last})/AVERAGE($C${first}:$C${last}),0)"), "평균 대비 흔들림의 크기입니다."),
				row("상위 25%", won("=PERCENTILE($C${first}:$C${last},0.75)")),
				row("2위 매출", won("=LARGE($C${first}:$C${last},2)")),
				row("최저 매출", won("=SMALL($C${first}:$C${last},1)")),
				row("광고비 상관계수", dec("=CORREL($B${first}:$B${last},$C${first}:$C${last})"), "1에 가까울수록 함께 움직입니다."),
				row("회귀 기울기", dec("=SLOPE($C${first}:$C${last},$B${first}:$B${last})"), "광고비 1원당 매출 증가분입니다."),
				row("회귀 절편", won("=INTERCEPT($C${first}:$C${last},$B${first}:$B${last})")),
				row("광고비 450만 예측", won("=FORECAST(4500000,$C${first}:$C${last},$B${first}:$B${last})"), "회귀선을 이용한 예측값입니다."),
			),
	),
	tmpl("formula-dynamic-array", "동적 배열 수식", "수식·함수", "UNIQUE·SORT·FILTER·SEQUENCE가 한 수식으로 여러 셀을 채우는 방식을 보여 줍니다.",
		sheet("원본").tab("#0f766e").cols(110, 110, 120, 120, 110).
			title("원본 데이터").note("이 표를 고치면 옆 시트의 배열 수식 결과가 함께 바뀝니다.").
			head("지역", "채널", "매출", "수량", "담당").
			rows(
				row("서울", "온라인", 4200000, 62, "김수현"),
				row("부산", "오프라인", 1850000, 24, "박지훈"),
				row("서울", "오프라인", 3100000, 41, "이서연"),
				row("대구", "온라인", 970000, 15, "김수현"),
				row("서울", "온라인", 5600000, 88, "박지훈"),
				row("부산", "온라인", 2450000, 33, "이서연"),
			).
			format(formatMoney, 3).format(formatNumber, 4).
			summary(row("합계", won("=SUM($C${first}:$C${last})"))),
		sheet("배열").tab("#5268a6").cols(150, 150, 170, 110, 110).
			title("동적 배열 수식").note("아래 한 줄의 수식이 아래쪽 셀까지 결과를 펼칩니다. 결과 셀은 직접 고칠 수 없습니다.").
			head("고유 지역", "매출 내림차순", "1000만 이상 지역", "번호").
			rows(
				row("=UNIQUE('원본'!A4:A9)", "=SORT('원본'!C4:C9,1,FALSE)", "=FILTER('원본'!A4:A9,'원본'!C4:C9>=3000000)", "=SEQUENCE(6)"),
			),
	),
	tmpl("formula-auto-range", "자동 확장 범위", "수식·함수", "A:A 형태의 열 전체 참조로 행을 추가해도 합계가 저절로 따라오게 만듭니다.",
		sheet("입력").tab("#c2703d").cols(120, 160, 130, 120).
			title("지출 입력").note("맨 아래에 행을 계속 추가해 보세요. 옆 시트의 집계가 범위를 고치지 않아도 따라옵니다.").
			head("일자", "항목", "금액", "분류").
			rows(
				row("2026-08-01", "사무용품", 128000, "운영"),
				row("2026-08-03", "클라우드 요금", 940000, "인프라"),
				row("2026-08-07", "출장 항공", 620000, "영업"),
				row("2026-08-12", "광고 집행", 2400000, "마케팅"),
				row("2026-08-19", "서버 증설", 1750000, "인프라"),
				row("2026-08-24", "고객 미팅", 86000, "영업"),
			).
			format(formatDate, 1).format(formatMoney, 3).
			summary(row("입력 건수", "=COUNTA($B${first}:$B${last})")),
		sheet("집계").tab("#0f766e").cols(170, 150, 330, 110).
			title("자동 확장 집계").note("수식이 '입력' 시트의 열 전체를 가리키므로 행을 더해도 범위를 고칠 필요가 없습니다.").
			head("항목", "값", "쓰인 수식").
			rows(
				row("총 지출", won("=SUM('입력'!C:C)"), "SUM('입력'!C:C)"),
				row("건수", "=COUNT('입력'!C:C)", "COUNT('입력'!C:C)"),
				row("평균 지출", won("=IFERROR(AVERAGE('입력'!C:C),0)"), "IFERROR(AVERAGE('입력'!C:C),0)"),
				row("최대 지출", won("=MAX('입력'!C:C)"), "MAX('입력'!C:C)"),
				row("인프라 지출", won("=SUMIF('입력'!D:D,\"인프라\",'입력'!C:C)"), "SUMIF('입력'!D:D,\"인프라\",'입력'!C:C)"),
				row("영업 건수", "=COUNTIF('입력'!D:D,\"영업\")", "COUNTIF('입력'!D:D,\"영업\")"),
				row("100만 이상", "=COUNTIF('입력'!C:C,\">=1000000\")", "COUNTIF('입력'!C:C,\">=1000000\")"),
				row("분류 수", "=COUNTUNIQUE('입력'!D4:D)", "COUNTUNIQUE('입력'!D4:D) — 머리글을 뺀 열린 범위"),
			).
			format(formatMoney, 2),
	),
	tmpl("formula-logic", "논리·오류 처리", "수식·함수", "IFS·SWITCH로 조건을 정리하고 IFERROR·IFNA로 오류를 감춥니다.",
		sheet("논리").tab("#8b5cf6").cols(120, 110, 110, 120, 130, 240).
			title("논리·오류 처리").note("점수와 코드에 따라 등급과 안내 문구를 자동으로 정합니다.").
			head("이름", "점수", "코드", "등급", "구분", "상태").
			rows(
				row("김수현", 92, "A", "=IFS(B{r}>=90,\"우수\",B{r}>=75,\"양호\",B{r}>=60,\"보통\",TRUE,\"미흡\")", "=SWITCH(C{r},\"A\",\"정직원\",\"B\",\"계약직\",\"C\",\"협력사\",\"미분류\")", "=IF(AND(B{r}>=75,C{r}=\"A\"),\"승급 대상\",\"유지\")"),
				row("박지훈", 78, "B", "=IFS(B{r}>=90,\"우수\",B{r}>=75,\"양호\",B{r}>=60,\"보통\",TRUE,\"미흡\")", "=SWITCH(C{r},\"A\",\"정직원\",\"B\",\"계약직\",\"C\",\"협력사\",\"미분류\")", "=IF(AND(B{r}>=75,C{r}=\"A\"),\"승급 대상\",\"유지\")"),
				row("이서연", 64, "A", "=IFS(B{r}>=90,\"우수\",B{r}>=75,\"양호\",B{r}>=60,\"보통\",TRUE,\"미흡\")", "=SWITCH(C{r},\"A\",\"정직원\",\"B\",\"계약직\",\"C\",\"협력사\",\"미분류\")", "=IF(AND(B{r}>=75,C{r}=\"A\"),\"승급 대상\",\"유지\")"),
				row("최민준", 51, "C", "=IFS(B{r}>=90,\"우수\",B{r}>=75,\"양호\",B{r}>=60,\"보통\",TRUE,\"미흡\")", "=SWITCH(C{r},\"A\",\"정직원\",\"B\",\"계약직\",\"C\",\"협력사\",\"미분류\")", "=IF(AND(B{r}>=75,C{r}=\"A\"),\"승급 대상\",\"유지\")"),
				row("한지우", 88, "A", "=IFS(B{r}>=90,\"우수\",B{r}>=75,\"양호\",B{r}>=60,\"보통\",TRUE,\"미흡\")", "=SWITCH(C{r},\"A\",\"정직원\",\"B\",\"계약직\",\"C\",\"협력사\",\"미분류\")", "=IF(AND(B{r}>=75,C{r}=\"A\"),\"승급 대상\",\"유지\")"),
			).
			format(formatNumber, 2).
			summary(
				row("우수 인원", "=COUNTIF($D${first}:$D${last},\"우수\")"),
				row("나눗셈 오류 처리", "=IFERROR(1/0,\"계산 불가\")", "IFERROR는 모든 오류를 대체합니다."),
				row("조회 실패 처리", "=IFNA(XLOOKUP(\"없는이름\",$A${first}:$A${last},$B${first}:$B${last}),\"명단 없음\")", "IFNA는 #N/A만 대체해 다른 오류는 그대로 드러냅니다."),
				row("숫자 여부", "=ISNUMBER($B${first})", "값의 종류를 검사합니다."),
				row("빈 셀 여부", "=ISBLANK($F$20)", "아래 빈 셀을 검사한 결과입니다."),
				row("배타적 조건", "=XOR($B${first}>=90,$C${first}=\"B\")", "둘 중 하나만 참일 때 TRUE입니다."),
				row("점수 구간 표시", "=SWITCH(TRUE,$B${first}>=90,\"90점대\",$B${first}>=80,\"80점대\",\"그 이하\")", "SWITCH에 TRUE를 넣으면 조건식도 쓸 수 있습니다."),
			),
	),
}
