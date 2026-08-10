package workbook

// The catalog is grouped by the work people actually do. Every template ships
// with sample rows so the formulas calculate the moment the workbook opens, and
// only functions the kanpic formula engine supports are used.
var templates = []Template{
	// 재무·회계 ---------------------------------------------------------------
	tmpl("monthly-pnl", "월간 손익계산서", "재무·회계", "매출과 비용을 항목별로 모아 영업이익과 이익률까지 계산합니다.",
		sheet("손익").tab("#0f766e").cols(150, 90, 120, 120, 110, 90).
			title("월간 손익계산서").note("구분에 수익 또는 비용을 적으면 아래 요약이 자동으로 집계됩니다.").
			head("항목", "구분", "전월", "당월", "증감", "증감율").
			rows(
				row("제품 매출", "수익", 42000000, 48500000, "=D{r}-C{r}", "=IFERROR((D{r}-C{r})/C{r},0)"),
				row("서비스 매출", "수익", 12500000, 13900000, "=D{r}-C{r}", "=IFERROR((D{r}-C{r})/C{r},0)"),
				row("매출원가", "비용", 21800000, 24300000, "=D{r}-C{r}", "=IFERROR((D{r}-C{r})/C{r},0)"),
				row("인건비", "비용", 18200000, 18900000, "=D{r}-C{r}", "=IFERROR((D{r}-C{r})/C{r},0)"),
				row("임차료", "비용", 3200000, 3200000, "=D{r}-C{r}", "=IFERROR((D{r}-C{r})/C{r},0)"),
				row("마케팅비", "비용", 4100000, 5600000, "=D{r}-C{r}", "=IFERROR((D{r}-C{r})/C{r},0)"),
				row("기타 판관비", "비용", 2400000, 2150000, "=D{r}-C{r}", "=IFERROR((D{r}-C{r})/C{r},0)"),
			).
			format(formatMoney, 3, 4, 5).format(formatPercent, 6).
			summary(
				row("매출 합계", won("=SUMIF(B{first}:B{last},\"수익\",D{first}:D{last})")),
				row("비용 합계", won("=SUMIF(B{first}:B{last},\"비용\",D{first}:D{last})")),
				row("영업이익", won("=SUMIF(B{first}:B{last},\"수익\",D{first}:D{last})-SUMIF(B{first}:B{last},\"비용\",D{first}:D{last})")),
				row("영업이익률", pct("=IFERROR((SUMIF(B{first}:B{last},\"수익\",D{first}:D{last})-SUMIF(B{first}:B{last},\"비용\",D{first}:D{last}))/SUMIF(B{first}:B{last},\"수익\",D{first}:D{last}),0)")),
			),
	),
	tmpl("cash-flow", "현금흐름 관리", "재무·회계", "입출금을 적으면 잔액이 자동으로 이어져 자금 상황을 한눈에 봅니다.",
		sheet("현금흐름").tab("#0f766e").cols(110, 90, 190, 120, 120, 130).
			title("현금흐름 관리").note("기초 잔액을 첫 행의 잔액에 넣고 이후에는 입금과 출금만 입력하세요.").
			head("일자", "구분", "내역", "입금", "출금", "잔액").
			rows(
				row("2026-08-01", "기초", "전월 이월", 0, 0, 32500000),
				row("2026-08-03", "매출", "A사 대금 입금", 12000000, 0, "=F{p}+D{r}-E{r}"),
				row("2026-08-05", "매입", "원자재 결제", 0, 4800000, "=F{p}+D{r}-E{r}"),
				row("2026-08-10", "급여", "8월 급여 지급", 0, 18900000, "=F{p}+D{r}-E{r}"),
				row("2026-08-14", "매출", "B사 대금 입금", 9500000, 0, "=F{p}+D{r}-E{r}"),
				row("2026-08-20", "고정비", "임차료·관리비", 0, 3600000, "=F{p}+D{r}-E{r}"),
				row("2026-08-25", "매출", "C사 대금 입금", 7400000, 0, "=F{p}+D{r}-E{r}"),
			).
			total("합계", nil, nil, "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", "=F{last}").
			format(formatDate, 1).format(formatMoney, 4, 5, 6).
			summary(
				row("순증감", "=SUM(D{first}:D{last})-SUM(E{first}:E{last})"),
				row("최저 잔액", "=MIN(F{first}:F{last})"),
			),
	),
	tmpl("budget-actual", "예산 대비 실적", "재무·회계", "부서별 예산과 집행액을 비교해 잔여 예산과 집행률을 보여 줍니다.",
		sheet("예산관리").tab("#0f766e").cols(130, 140, 120, 120, 120, 90, 90).
			title("예산 대비 실적").note("집행액만 갱신하면 잔여 예산과 집행률, 초과 여부가 자동 반영됩니다.").
			head("부서", "예산 항목", "연간 예산", "집행액", "잔여", "집행률", "상태").
			rows(
				row("영업본부", "출장비", 24000000, 15600000, "=C{r}-D{r}", "=IFERROR(D{r}/C{r},0)", "=IF(D{r}>C{r},\"초과\",IF(D{r}/C{r}>0.9,\"주의\",\"정상\"))"),
				row("영업본부", "판촉비", 36000000, 33800000, "=C{r}-D{r}", "=IFERROR(D{r}/C{r},0)", "=IF(D{r}>C{r},\"초과\",IF(D{r}/C{r}>0.9,\"주의\",\"정상\"))"),
				row("개발본부", "라이선스", 18000000, 9200000, "=C{r}-D{r}", "=IFERROR(D{r}/C{r},0)", "=IF(D{r}>C{r},\"초과\",IF(D{r}/C{r}>0.9,\"주의\",\"정상\"))"),
				row("개발본부", "장비 구매", 42000000, 44100000, "=C{r}-D{r}", "=IFERROR(D{r}/C{r},0)", "=IF(D{r}>C{r},\"초과\",IF(D{r}/C{r}>0.9,\"주의\",\"정상\"))"),
				row("경영지원", "교육비", 12000000, 4300000, "=C{r}-D{r}", "=IFERROR(D{r}/C{r},0)", "=IF(D{r}>C{r},\"초과\",IF(D{r}/C{r}>0.9,\"주의\",\"정상\"))"),
				row("경영지원", "복리후생", 30000000, 21500000, "=C{r}-D{r}", "=IFERROR(D{r}/C{r},0)", "=IF(D{r}>C{r},\"초과\",IF(D{r}/C{r}>0.9,\"주의\",\"정상\"))"),
			).
			total("합계", nil, "=SUM(C{first}:C{last})", "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", "=IFERROR(SUM(D{first}:D{last})/SUM(C{first}:C{last}),0)", nil).
			format(formatMoney, 3, 4, 5).format(formatPercent, 6).
			summary(row("초과 항목 수", "=COUNTIF(G{first}:G{last},\"초과\")")),
	),
	tmpl("invoice", "거래명세서", "재무·회계", "품목별 공급가와 부가세를 계산해 청구 총액까지 정리합니다.",
		sheet("거래명세서").tab("#5268a6").cols(60, 200, 80, 110, 130, 110, 130).
			title("거래명세서").note("수량과 단가만 입력하면 공급가액·부가세·합계가 계산됩니다.").
			head("No", "품목", "수량", "단가", "공급가액", "부가세", "합계").
			rows(
				row(1, "웹 대시보드 구축", 1, 18000000, "=C{r}*D{r}", "=ROUND(E{r}*0.1,0)", "=E{r}+F{r}"),
				row(2, "데이터 마이그레이션", 1, 6500000, "=C{r}*D{r}", "=ROUND(E{r}*0.1,0)", "=E{r}+F{r}"),
				row(3, "운영 교육 (인/일)", 12, 350000, "=C{r}*D{r}", "=ROUND(E{r}*0.1,0)", "=E{r}+F{r}"),
				row(4, "유지보수 (월)", 6, 1200000, "=C{r}*D{r}", "=ROUND(E{r}*0.1,0)", "=E{r}+F{r}"),
			).
			total("합계", nil, "=SUM(C{first}:C{last})", nil, "=SUM(E{first}:E{last})", "=SUM(F{first}:F{last})", "=SUM(G{first}:G{last})").
			format(formatNumber, 3).format(formatMoney, 4, 5, 6, 7).
			summary(
				row("청구 총액", won("=SUM(G{first}:G{last})")),
				row("결제 기한", "2026-09-15"),
			),
	),
	tmpl("quotation", "견적서", "재무·회계", "할인율을 반영한 견적 금액과 유효기간을 관리합니다.",
		sheet("견적서").tab("#5268a6").cols(60, 200, 80, 120, 90, 130, 130).
			title("견적서").note("할인율은 0.05처럼 비율로 입력하세요. 견적가는 자동 계산됩니다.").
			head("No", "항목", "수량", "단가", "할인율", "견적가", "비고").
			rows(
				row(1, "기본 구축비", 1, 22000000, 0.05, "=ROUND(C{r}*D{r}*(1-E{r}),0)", "표준 범위"),
				row(2, "추가 개발 (MD)", 25, 620000, 0.0, "=ROUND(C{r}*D{r}*(1-E{r}),0)", "요구사항 확정 후"),
				row(3, "서버 구성", 3, 2400000, 0.1, "=ROUND(C{r}*D{r}*(1-E{r}),0)", "3년 약정"),
				row(4, "연간 유지보수", 1, 9600000, 0.0, "=ROUND(C{r}*D{r}*(1-E{r}),0)", "구축 완료 후"),
			).
			total("합계", nil, nil, nil, nil, "=SUM(F{first}:F{last})", nil).
			format(formatNumber, 3).format(formatMoney, 4, 6).format(formatPercent, 5).
			summary(
				row("부가세", won("=ROUND(SUM(F{first}:F{last})*0.1,0)")),
				row("총 견적금액", won("=SUM(F{first}:F{last})+ROUND(SUM(F{first}:F{last})*0.1,0)")),
				row("견적 유효기간", "2026-09-30"),
			),
	),
	tmpl("expense-report", "경비 정산서", "재무·회계", "지출 내역을 분류별로 모아 정산 금액과 승인 상태를 관리합니다.",
		sheet("경비정산").tab("#d97706").cols(110, 110, 190, 120, 100, 100).
			title("경비 정산서").note("영수증 보관 여부와 승인 상태를 함께 관리하면 결재가 빨라집니다.").
			head("사용일", "분류", "사용 내역", "금액", "영수증", "승인").
			rows(
				row("2026-08-02", "교통비", "고객사 방문 (KTX)", 118000, "있음", "승인"),
				row("2026-08-04", "식대", "팀 점심 (4인)", 68000, "있음", "승인"),
				row("2026-08-07", "숙박비", "부산 출장 1박", 145000, "있음", "대기"),
				row("2026-08-11", "소모품", "회의용 문구", 32500, "없음", "대기"),
				row("2026-08-18", "접대비", "협력사 미팅", 210000, "있음", "반려"),
			).
			total("합계", nil, nil, "=SUM(D{first}:D{last})", nil, nil).
			format(formatDate, 1).format(formatMoney, 4).
			summary(
				row("승인 금액", won("=SUMIF(F{first}:F{last},\"승인\",D{first}:D{last})")),
				row("대기 금액", won("=SUMIF(F{first}:F{last},\"대기\",D{first}:D{last})")),
				row("영수증 누락 건수", "=COUNTIF(E{first}:E{last},\"없음\")"),
			),
	),

	// 영업·마케팅 -------------------------------------------------------------
	tmpl("sales-pipeline", "영업 파이프라인", "영업·마케팅", "단계별 수주 확률을 반영한 가중 예상 매출을 계산합니다.",
		sheet("파이프라인").tab("#3b82f6").cols(160, 120, 110, 130, 90, 130, 110).
			title("영업 파이프라인").note("확률은 단계에 맞춰 0~1 사이 값으로 입력하면 가중금액이 계산됩니다.").
			head("고객사", "담당", "단계", "예상 금액", "확률", "가중 금액", "마감 예정").
			rows(
				row("가온테크", "박지민", "제안", 48000000, 0.4, "=ROUND(D{r}*E{r},0)", "2026-09-30"),
				row("한빛물류", "이서준", "협상", 92000000, 0.7, "=ROUND(D{r}*E{r},0)", "2026-09-15"),
				row("미래에너지", "박지민", "발굴", 35000000, 0.2, "=ROUND(D{r}*E{r},0)", "2026-11-20"),
				row("성진산업", "최수아", "계약", 61000000, 0.9, "=ROUND(D{r}*E{r},0)", "2026-08-31"),
				row("대현식품", "이서준", "제안", 27000000, 0.4, "=ROUND(D{r}*E{r},0)", "2026-10-10"),
				row("우성건설", "최수아", "발굴", 120000000, 0.2, "=ROUND(D{r}*E{r},0)", "2026-12-15"),
			).
			total("합계", nil, nil, "=SUM(D{first}:D{last})", nil, "=SUM(F{first}:F{last})", nil).
			format(formatMoney, 4, 6).format(formatPercent, 5).format(formatDate, 7).
			summary(
				row("계약 단계 금액", won("=SUMIF(C{first}:C{last},\"계약\",D{first}:D{last})")),
				row("건수", "=COUNTA(A{first}:A{last})"),
				row("평균 예상 금액", won("=ROUND(AVERAGE(D{first}:D{last}),0)")),
			),
	),
	tmpl("crm-contacts", "고객 관리 대장", "영업·마케팅", "고객 정보와 최근 접촉 이력, 등급을 한 곳에서 관리합니다.",
		sheet("고객").tab("#3b82f6").cols(150, 110, 130, 190, 110, 110, 90).
			title("고객 관리 대장").note("등급과 다음 접촉 예정일을 기준으로 후속 활동을 계획하세요.").
			head("고객사", "담당자", "연락처", "이메일", "최근 접촉", "다음 접촉", "등급").
			rows(
				row("가온테크", "김도현", "02-555-0182", "dh.kim@gaon.example", "2026-08-01", "2026-08-20", "A"),
				row("한빛물류", "정유진", "031-777-2043", "yj.jung@hanbit.example", "2026-07-28", "2026-08-18", "A"),
				row("미래에너지", "오세훈", "051-330-9911", "sh.oh@mirae.example", "2026-07-15", "2026-09-01", "B"),
				row("성진산업", "장하늘", "053-244-7788", "hn.jang@sungjin.example", "2026-08-05", "2026-08-25", "A"),
				row("대현식품", "문서영", "062-812-3355", "sy.moon@daehyun.example", "2026-06-30", "2026-09-10", "C"),
			).
			format(formatDate, 5, 6).
			summary(
				row("A등급 고객 수", "=COUNTIF(G{first}:G{last},\"A\")"),
				row("전체 고객 수", "=COUNTA(A{first}:A{last})"),
			),
	),
	tmpl("campaign-performance", "마케팅 캠페인 성과", "영업·마케팅", "노출과 클릭에서 CTR, CPA, ROAS까지 자동으로 계산합니다.",
		sheet("캠페인").tab("#8b5cf6").cols(160, 100, 110, 100, 80, 100, 130, 110, 90).
			title("마케팅 캠페인 성과").note("노출·클릭·전환·비용·매출만 입력하면 나머지 지표는 계산됩니다.").
			head("캠페인", "채널", "노출", "클릭", "CTR", "전환", "비용", "매출", "ROAS").
			rows(
				row("여름 프로모션", "검색", 420000, 12600, "=IFERROR(D{r}/C{r},0)", 380, 7200000, 41800000, "=IFERROR(H{r}/G{r},0)"),
				row("신규 가입 유도", "SNS", 850000, 21250, "=IFERROR(D{r}/C{r},0)", 460, 9500000, 33200000, "=IFERROR(H{r}/G{r},0)"),
				row("리타게팅", "디스플레이", 1250000, 8750, "=IFERROR(D{r}/C{r},0)", 210, 4300000, 18600000, "=IFERROR(H{r}/G{r},0)"),
				row("업계 뉴스레터", "이메일", 96000, 7680, "=IFERROR(D{r}/C{r},0)", 320, 1200000, 22400000, "=IFERROR(H{r}/G{r},0)"),
			).
			total("합계", nil, "=SUM(C{first}:C{last})", "=SUM(D{first}:D{last})", "=IFERROR(SUM(D{first}:D{last})/SUM(C{first}:C{last}),0)", "=SUM(F{first}:F{last})", "=SUM(G{first}:G{last})", "=SUM(H{first}:H{last})", "=IFERROR(SUM(H{first}:H{last})/SUM(G{first}:G{last}),0)").
			format(formatNumber, 3, 4, 6).format(formatPercent, 5).format(formatMoney, 7, 8).format(formatDecimal, 9).
			summary(
				row("전환당 비용(CPA)", won("=IFERROR(ROUND(SUM(G{first}:G{last})/SUM(F{first}:F{last}),0),0)")),
				row("최고 ROAS 캠페인", "=INDEX(A{first}:A{last},MATCH(MAX(I{first}:I{last}),I{first}:I{last},0))"),
			),
	),
	tmpl("monthly-sales", "월간 매출 분석", "영업·마케팅", "제품별 월 매출과 비중, 전월 대비 증감을 정리합니다.",
		sheet("매출분석").tab("#22c55e").cols(150, 130, 130, 120, 100, 100).
			title("월간 매출 분석").note("제품별 실적만 입력하면 증감과 매출 비중이 계산됩니다.").
			head("제품", "전월 매출", "당월 매출", "증감", "증감율", "비중").
			rows(
				row("클라우드 구독", 128000000, 141500000, "=C{r}-B{r}", "=IFERROR((C{r}-B{r})/B{r},0)", "=IFERROR(C{r}/SUM(C{first}:C{last}),0)"),
				row("온프레미스 라이선스", 86000000, 79400000, "=C{r}-B{r}", "=IFERROR((C{r}-B{r})/B{r},0)", "=IFERROR(C{r}/SUM(C{first}:C{last}),0)"),
				row("컨설팅", 41000000, 52800000, "=C{r}-B{r}", "=IFERROR((C{r}-B{r})/B{r},0)", "=IFERROR(C{r}/SUM(C{first}:C{last}),0)"),
				row("교육", 12500000, 11200000, "=C{r}-B{r}", "=IFERROR((C{r}-B{r})/B{r},0)", "=IFERROR(C{r}/SUM(C{first}:C{last}),0)"),
				row("기술지원", 33400000, 36900000, "=C{r}-B{r}", "=IFERROR((C{r}-B{r})/B{r},0)", "=IFERROR(C{r}/SUM(C{first}:C{last}),0)"),
			).
			total("합계", "=SUM(B{first}:B{last})", "=SUM(C{first}:C{last})", "=SUM(D{first}:D{last})", "=IFERROR(SUM(D{first}:D{last})/SUM(B{first}:B{last}),0)", 1).
			format(formatMoney, 2, 3, 4).format(formatPercent, 5, 6).
			summary(
				row("최대 매출 제품", "=INDEX(A{first}:A{last},MATCH(MAX(C{first}:C{last}),C{first}:C{last},0))"),
				row("평균 매출", won("=ROUND(AVERAGE(C{first}:C{last}),0)")),
			),
	),
	tmpl("kpi-dashboard", "KPI 대시보드", "영업·마케팅", "목표와 실적을 비교해 달성률과 신호등 상태를 보여 줍니다.",
		sheet("KPI").tab("#22c55e").cols(180, 110, 120, 120, 100, 100).
			title("KPI 대시보드").note("달성률 90% 이상은 정상, 70% 이상은 주의, 그 아래는 위험으로 표시됩니다.").
			head("지표", "단위", "목표", "실적", "달성률", "상태").
			rows(
				row("신규 계약 건수", "건", 24, 21, "=IFERROR(D{r}/C{r},0)", "=IF(E{r}>=0.9,\"정상\",IF(E{r}>=0.7,\"주의\",\"위험\"))"),
				row("월 매출", "원", 320000000, 341800000, "=IFERROR(D{r}/C{r},0)", "=IF(E{r}>=0.9,\"정상\",IF(E{r}>=0.7,\"주의\",\"위험\"))"),
				row("신규 가입자", "명", 5000, 3400, "=IFERROR(D{r}/C{r},0)", "=IF(E{r}>=0.9,\"정상\",IF(E{r}>=0.7,\"주의\",\"위험\"))"),
				row("고객 이탈률", "%", 0.03, 0.041, "=IFERROR(C{r}/D{r},0)", "=IF(E{r}>=0.9,\"정상\",IF(E{r}>=0.7,\"주의\",\"위험\"))"),
				row("평균 응대 시간", "분", 15, 12, "=IFERROR(C{r}/D{r},0)", "=IF(E{r}>=0.9,\"정상\",IF(E{r}>=0.7,\"주의\",\"위험\"))"),
			).
			format(formatPercent, 5).
			summary(
				row("위험 지표 수", "=COUNTIF(F{first}:F{last},\"위험\")"),
				row("평균 달성률", pct("=ROUND(AVERAGE(E{first}:E{last}),3)")),
			),
	),

	// 프로젝트 ---------------------------------------------------------------
	tmpl("project-status", "프로젝트 현황", "프로젝트", "업무별 담당자와 진행률을 모아 프로젝트 전체 상황을 봅니다.",
		sheet("현황").tab("#0891b2").cols(190, 110, 110, 110, 100, 100, 150).
			title("프로젝트 현황").note("진행률은 0~1 사이 값으로 입력하세요. 상태는 진행률에 따라 자동 표시됩니다.").
			head("업무", "담당", "시작일", "종료일", "진행률", "상태", "비고").
			rows(
				row("요구사항 정의", "박지민", "2026-08-03", "2026-08-14", 1.0, "=IF(E{r}>=1,\"완료\",IF(E{r}>0,\"진행중\",\"대기\"))", "승인 완료"),
				row("화면 설계", "최수아", "2026-08-10", "2026-08-28", 0.75, "=IF(E{r}>=1,\"완료\",IF(E{r}>0,\"진행중\",\"대기\"))", "리뷰 2회 남음"),
				row("API 개발", "이서준", "2026-08-17", "2026-09-25", 0.4, "=IF(E{r}>=1,\"완료\",IF(E{r}>0,\"진행중\",\"대기\"))", nil),
				row("데이터 이관", "정유진", "2026-09-07", "2026-09-30", 0.0, "=IF(E{r}>=1,\"완료\",IF(E{r}>0,\"진행중\",\"대기\"))", "리허설 필요"),
				row("통합 테스트", "김도현", "2026-09-21", "2026-10-09", 0.0, "=IF(E{r}>=1,\"완료\",IF(E{r}>0,\"진행중\",\"대기\"))", nil),
				row("오픈 준비", "박지민", "2026-10-12", "2026-10-23", 0.0, "=IF(E{r}>=1,\"완료\",IF(E{r}>0,\"진행중\",\"대기\"))", nil),
			).
			format(formatDate, 3, 4).format(formatPercent, 5).
			summary(
				row("전체 진행률", pct("=ROUND(AVERAGE(E{first}:E{last}),3)")),
				row("완료 업무", "=COUNTIF(F{first}:F{last},\"완료\")"),
				row("진행중 업무", "=COUNTIF(F{first}:F{last},\"진행중\")"),
			),
	),
	tmpl("gantt-schedule", "프로젝트 일정표", "프로젝트", "단계별 기간과 투입 공수, 남은 공수를 계산합니다.",
		sheet("일정").tab("#0891b2").cols(170, 110, 110, 90, 90, 100, 90, 110).
			title("프로젝트 일정표").note("기간(일)과 인원을 입력하면 공수(MD)와 진행률 기준 잔여 공수가 계산됩니다.").
			head("단계", "시작일", "종료일", "기간(일)", "인원", "공수(MD)", "진행률", "잔여(MD)").
			rows(
				row("착수 및 계획", "2026-08-03", "2026-08-07", 5, 2, "=D{r}*E{r}", 1.0, "=ROUND(F{r}*(1-G{r}),1)"),
				row("분석", "2026-08-10", "2026-08-21", 10, 3, "=D{r}*E{r}", 0.8, "=ROUND(F{r}*(1-G{r}),1)"),
				row("설계", "2026-08-24", "2026-09-11", 15, 3, "=D{r}*E{r}", 0.35, "=ROUND(F{r}*(1-G{r}),1)"),
				row("개발", "2026-09-14", "2026-10-23", 30, 5, "=D{r}*E{r}", 0.0, "=ROUND(F{r}*(1-G{r}),1)"),
				row("테스트", "2026-10-26", "2026-11-06", 10, 4, "=D{r}*E{r}", 0.0, "=ROUND(F{r}*(1-G{r}),1)"),
				row("이행 및 안정화", "2026-11-09", "2026-11-20", 10, 3, "=D{r}*E{r}", 0.0, "=ROUND(F{r}*(1-G{r}),1)"),
			).
			total("합계", nil, nil, "=SUM(D{first}:D{last})", nil, "=SUM(F{first}:F{last})", "=ROUND(AVERAGE(G{first}:G{last}),3)", "=SUM(H{first}:H{last})").
			format(formatDate, 2, 3).format(formatNumber, 4, 5, 6).format(formatPercent, 7).format(formatDecimal, 8).
			summary(
				row("완료 단계", "=COUNTIF(G{first}:G{last},\">=1\")"),
				row("전체 공수 대비 잔여", "=IFERROR(SUM(H{first}:H{last})/SUM(F{first}:F{last}),0)"),
			),
	),
	tmpl("task-tracker", "업무 요청 관리", "프로젝트", "요청 접수부터 완료까지 우선순위와 상태로 추적합니다.",
		sheet("요청목록").tab("#f59e0b").cols(70, 200, 110, 110, 90, 110, 110).
			title("업무 요청 관리").note("상태는 대기·진행중·완료·보류 중에서 선택해 사용하세요.").
			head("No", "요청 내용", "요청자", "담당자", "우선순위", "기한", "상태").
			rows(
				row(1, "매출 리포트 자동화", "영업본부", "이서준", "높음", "2026-08-21", "진행중"),
				row(2, "회원 데이터 정합성 점검", "CS팀", "정유진", "중간", "2026-08-28", "대기"),
				row(3, "재고 알림 기준 변경", "물류팀", "김도현", "높음", "2026-08-18", "완료"),
				row(4, "권한 그룹 재정비", "경영지원", "최수아", "낮음", "2026-09-11", "보류"),
				row(5, "대시보드 지표 추가", "경영진", "박지민", "높음", "2026-09-04", "진행중"),
			).
			format(formatDate, 6).
			summary(
				row("전체 요청", "=COUNTA(A{first}:A{last})"),
				row("완료", "=COUNTIF(G{first}:G{last},\"완료\")"),
				row("진행중", "=COUNTIF(G{first}:G{last},\"진행중\")"),
				row("높은 우선순위 미완료", "=COUNTIFS(E{first}:E{last},\"높음\",G{first}:G{last},\"진행중\")"),
			),
	),
	tmpl("meeting-minutes", "회의록", "프로젝트", "안건과 결정 사항, 실행 항목을 담당자와 기한까지 남깁니다.",
		sheet("회의록").tab("#f59e0b").cols(70, 200, 220, 110, 110, 90).
			title("회의록").note("결정 사항과 실행 항목을 분리해 적으면 후속 관리가 쉬워집니다.").
			head("No", "안건", "논의 및 결정", "담당자", "기한", "상태").
			rows(
				row(1, "9월 릴리즈 범위 확정", "핵심 3개 기능만 포함하고 나머지는 10월로 이월", "박지민", "2026-08-21", "완료"),
				row(2, "성능 이슈 대응", "쿼리 튜닝 우선, 캐시 도입은 재검토", "이서준", "2026-08-28", "진행중"),
				row(3, "고객 교육 일정", "9월 둘째 주 2회 진행, 자료는 CS팀 준비", "정유진", "2026-09-04", "대기"),
				row(4, "장애 대응 절차", "당직 순번표 작성 후 공유", "김도현", "2026-08-25", "진행중"),
			).
			format(formatDate, 5).
			summary(
				row("실행 항목 수", "=COUNTA(A{first}:A{last})"),
				row("미완료", "=COUNTA(A{first}:A{last})-COUNTIF(F{first}:F{last},\"완료\")"),
			),
	),
	tmpl("risk-register", "리스크 관리 대장", "프로젝트", "발생 가능성과 영향도를 곱해 위험도와 대응 우선순위를 정합니다.",
		sheet("리스크").tab("#dc4f4f").cols(70, 200, 90, 90, 90, 90, 190, 110).
			title("리스크 관리 대장").note("가능성과 영향도를 1~5로 입력하면 위험도와 등급이 계산됩니다.").
			head("No", "리스크", "가능성", "영향도", "위험도", "등급", "대응 방안", "담당").
			rows(
				row(1, "핵심 인력 이탈", 3, 5, "=C{r}*D{r}", "=IF(E{r}>=15,\"높음\",IF(E{r}>=8,\"중간\",\"낮음\"))", "업무 문서화 및 백업 담당 지정", "박지민"),
				row(2, "요구사항 잦은 변경", 4, 4, "=C{r}*D{r}", "=IF(E{r}>=15,\"높음\",IF(E{r}>=8,\"중간\",\"낮음\"))", "변경관리 절차와 주간 확정 회의", "최수아"),
				row(3, "외부 API 지연", 2, 4, "=C{r}*D{r}", "=IF(E{r}>=15,\"높음\",IF(E{r}>=8,\"중간\",\"낮음\"))", "대체 경로 확보 및 타임아웃 설계", "이서준"),
				row(4, "데이터 이관 오류", 3, 5, "=C{r}*D{r}", "=IF(E{r}>=15,\"높음\",IF(E{r}>=8,\"중간\",\"낮음\"))", "리허설 2회와 롤백 계획 수립", "정유진"),
				row(5, "예산 초과", 2, 3, "=C{r}*D{r}", "=IF(E{r}>=15,\"높음\",IF(E{r}>=8,\"중간\",\"낮음\"))", "월간 집행 점검", "김도현"),
			).
			summary(
				row("높음 등급", "=COUNTIF(F{first}:F{last},\"높음\")"),
				row("평균 위험도", "=ROUND(AVERAGE(E{first}:E{last}),1)"),
				row("최고 위험 항목", "=INDEX(B{first}:B{last},MATCH(MAX(E{first}:E{last}),E{first}:E{last},0))"),
			),
	),

	// 인사 -------------------------------------------------------------------
	tmpl("attendance", "근태 관리", "인사", "출퇴근 시각과 휴게 시간으로 실근무와 연장근무를 계산합니다.",
		sheet("근태").tab("#ec4899").cols(110, 100, 90, 90, 90, 100, 100, 130).
			title("근태 관리").note("시각은 9.0, 18.5처럼 소수로 입력합니다. 실근무 8시간 초과분이 연장근무입니다.").
			head("일자", "이름", "출근", "퇴근", "휴게", "실근무", "연장", "비고").
			rows(
				row("2026-08-03", "박지민", 9.0, 18.5, 1.0, "=MAX(D{r}-C{r}-E{r},0)", "=MAX(F{r}-8,0)", nil),
				row("2026-08-04", "박지민", 8.5, 19.0, 1.0, "=MAX(D{r}-C{r}-E{r},0)", "=MAX(F{r}-8,0)", "긴급 대응"),
				row("2026-08-05", "박지민", 9.0, 17.5, 1.0, "=MAX(D{r}-C{r}-E{r},0)", "=MAX(F{r}-8,0)", nil),
				row("2026-08-06", "박지민", 9.0, 18.0, 1.0, "=MAX(D{r}-C{r}-E{r},0)", "=MAX(F{r}-8,0)", nil),
				row("2026-08-07", "박지민", 9.0, 21.0, 1.5, "=MAX(D{r}-C{r}-E{r},0)", "=MAX(F{r}-8,0)", "릴리즈"),
			).
			total("합계", nil, nil, nil, "=SUM(E{first}:E{last})", "=SUM(F{first}:F{last})", "=SUM(G{first}:G{last})", nil).
			format(formatDate, 1).format(formatDecimal, 3, 4, 5, 6, 7).
			summary(
				row("평균 실근무", "=ROUND(AVERAGE(F{first}:F{last}),2)"),
				row("연장근무 일수", "=COUNTIF(G{first}:G{last},\">0\")"),
			),
	),
	tmpl("payroll", "급여 대장", "인사", "기본급과 수당에서 공제를 빼고 실지급액을 계산합니다.",
		sheet("급여").tab("#ec4899").cols(100, 110, 130, 120, 120, 120, 130).
			title("급여 대장").note("공제는 4대보험과 소득세 합계를 입력하세요. 실지급액이 자동 계산됩니다.").
			head("사번", "이름", "기본급", "수당", "공제", "실지급액", "지급일").
			rows(
				row("E-1001", "박지민", 4200000, 350000, 612000, "=C{r}+D{r}-E{r}", "2026-08-25"),
				row("E-1002", "이서준", 3850000, 280000, 548000, "=C{r}+D{r}-E{r}", "2026-08-25"),
				row("E-1003", "최수아", 4600000, 420000, 703000, "=C{r}+D{r}-E{r}", "2026-08-25"),
				row("E-1004", "정유진", 3400000, 190000, 471000, "=C{r}+D{r}-E{r}", "2026-08-25"),
				row("E-1005", "김도현", 5100000, 500000, 812000, "=C{r}+D{r}-E{r}", "2026-08-25"),
			).
			total("합계", nil, "=SUM(C{first}:C{last})", "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", "=SUM(F{first}:F{last})", nil).
			format(formatMoney, 3, 4, 5, 6).format(formatDate, 7).
			summary(
				row("인원", "=COUNTA(A{first}:A{last})"),
				row("평균 실지급액", won("=ROUND(AVERAGE(F{first}:F{last}),0)")),
			),
	),
	tmpl("recruiting-tracker", "채용 진행 현황", "인사", "지원자별 전형 단계와 결과를 한 줄로 추적합니다.",
		sheet("채용").tab("#9333ea").cols(110, 140, 110, 110, 110, 100, 150).
			title("채용 진행 현황").note("단계는 서류·1차·2차·처우협의·입사확정 순으로 관리합니다.").
			head("지원일", "이름", "포지션", "단계", "면접일", "결과", "비고").
			rows(
				row("2026-07-20", "한지우", "백엔드 개발", "2차", "2026-08-12", "진행", "레퍼런스 확인 예정"),
				row("2026-07-24", "노기훈", "데이터 분석", "서류", nil, "진행", nil),
				row("2026-07-28", "서예린", "프론트엔드", "처우협의", "2026-08-06", "합격", "9월 1일 입사 예정"),
				row("2026-08-01", "임재현", "QA", "1차", "2026-08-14", "진행", nil),
				row("2026-08-03", "백은수", "백엔드 개발", "서류", nil, "불합격", "경력 미달"),
			).
			format(formatDate, 1, 5).
			summary(
				row("진행 중", "=COUNTIF(F{first}:F{last},\"진행\")"),
				row("합격", "=COUNTIF(F{first}:F{last},\"합격\")"),
				row("전체 지원자", "=COUNTA(B{first}:B{last})"),
			),
	),
	tmpl("employee-roster", "사원 명부", "인사", "부서와 직급, 입사일과 연락처를 정리한 인력 현황표입니다.",
		sheet("사원명부").tab("#9333ea").cols(100, 110, 120, 100, 110, 140, 180).
			title("사원 명부").note("부서와 직급 기준 인원 집계가 아래에 함께 계산됩니다.").
			head("사번", "이름", "부서", "직급", "입사일", "연락처", "이메일").
			rows(
				row("E-1001", "박지민", "영업본부", "부장", "2019-03-04", "010-2200-1101", "jm.park@corp.example"),
				row("E-1002", "이서준", "개발본부", "차장", "2020-07-13", "010-2200-1102", "sj.lee@corp.example"),
				row("E-1003", "최수아", "개발본부", "과장", "2021-01-11", "010-2200-1103", "sa.choi@corp.example"),
				row("E-1004", "정유진", "경영지원", "대리", "2022-09-05", "010-2200-1104", "yj.jung@corp.example"),
				row("E-1005", "김도현", "물류팀", "차장", "2018-11-19", "010-2200-1105", "dh.kim@corp.example"),
			).
			format(formatDate, 5).
			summary(
				row("총 인원", "=COUNTA(A{first}:A{last})"),
				row("개발본부 인원", "=COUNTIF(C{first}:C{last},\"개발본부\")"),
			),
	),
	tmpl("training-log", "교육 이수 관리", "인사", "필수 교육 이수 여부와 이수율을 관리합니다.",
		sheet("교육이수").tab("#06b6d4").cols(110, 170, 110, 110, 90, 100).
			title("교육 이수 관리").note("이수 여부에 완료 또는 미완료를 입력하면 이수율이 계산됩니다.").
			head("이름", "교육 과정", "구분", "이수일", "시간", "이수 여부").
			rows(
				row("박지민", "정보보호 기본", "필수", "2026-03-12", 2, "완료"),
				row("이서준", "정보보호 기본", "필수", "2026-03-12", 2, "완료"),
				row("최수아", "개인정보 보호", "필수", nil, 2, "미완료"),
				row("정유진", "직장 내 괴롭힘 예방", "법정", "2026-05-20", 1, "완료"),
				row("김도현", "안전보건 교육", "법정", nil, 4, "미완료"),
			).
			total("합계", nil, nil, nil, "=SUM(E{first}:E{last})", nil).
			format(formatDate, 4).format(formatDecimal, 5).
			summary(
				row("이수율", pct("=IFERROR(COUNTIF(F{first}:F{last},\"완료\")/COUNTA(F{first}:F{last}),0)")),
				row("미이수 인원", "=COUNTIF(F{first}:F{last},\"미완료\")"),
			),
	),

	// 운영·재고 --------------------------------------------------------------
	tmpl("inventory", "재고 관리", "운영·재고", "입출고를 반영한 현재고와 재주문 시점을 자동으로 알려 줍니다.",
		sheet("재고").tab("#d97706").cols(110, 170, 100, 90, 90, 90, 100, 110, 130).
			title("재고 관리").note("현재고가 재주문점 이하가 되면 상태에 재주문으로 표시됩니다.").
			head("품목코드", "품목명", "구분", "기초", "입고", "출고", "현재고", "재주문점", "상태").
			rows(
				row("SKU-1001", "A4 복사용지", "사무", 120, 200, 240, "=D{r}+E{r}-F{r}", 100, "=IF(G{r}<=H{r},\"재주문\",\"정상\")"),
				row("SKU-1002", "토너 카트리지", "사무", 40, 25, 38, "=D{r}+E{r}-F{r}", 30, "=IF(G{r}<=H{r},\"재주문\",\"정상\")"),
				row("SKU-2001", "포장 박스 (대)", "물류", 500, 300, 620, "=D{r}+E{r}-F{r}", 200, "=IF(G{r}<=H{r},\"재주문\",\"정상\")"),
				row("SKU-2002", "완충재", "물류", 300, 150, 410, "=D{r}+E{r}-F{r}", 150, "=IF(G{r}<=H{r},\"재주문\",\"정상\")"),
				row("SKU-3001", "노트북 배터리", "자재", 25, 10, 12, "=D{r}+E{r}-F{r}", 15, "=IF(G{r}<=H{r},\"재주문\",\"정상\")"),
			).
			total("합계", nil, nil, "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", "=SUM(F{first}:F{last})", "=SUM(G{first}:G{last})", nil, nil).
			format(formatNumber, 4, 5, 6, 7, 8).
			summary(
				row("재주문 품목 수", "=COUNTIF(I{first}:I{last},\"재주문\")"),
				row("품목 수", "=COUNTA(A{first}:A{last})"),
			),
	),
	tmpl("purchase-order", "발주 관리", "운영·재고", "발주 금액과 입고 예정일, 진행 상태를 관리합니다.",
		sheet("발주").tab("#d97706").cols(110, 130, 150, 80, 110, 130, 110, 90).
			title("발주 관리").note("수량과 단가를 입력하면 발주 금액이 계산됩니다.").
			head("발주번호", "발주일", "거래처", "수량", "단가", "발주 금액", "입고 예정", "상태").
			rows(
				row("PO-2608-01", "2026-08-03", "우성지류", 200, 4800, "=D{r}*E{r}", "2026-08-10", "입고완료"),
				row("PO-2608-02", "2026-08-05", "한빛물류", 500, 1250, "=D{r}*E{r}", "2026-08-14", "진행중"),
				row("PO-2608-03", "2026-08-08", "성진산업", 30, 86000, "=D{r}*E{r}", "2026-08-22", "진행중"),
				row("PO-2608-04", "2026-08-12", "미래전자", 15, 145000, "=D{r}*E{r}", "2026-08-30", "대기"),
			).
			total("합계", nil, nil, "=SUM(D{first}:D{last})", nil, "=SUM(F{first}:F{last})", nil, nil).
			format(formatDate, 2, 7).format(formatNumber, 4).format(formatMoney, 5, 6).
			summary(
				row("미입고 금액", won("=SUM(F{first}:F{last})-SUMIF(H{first}:H{last},\"입고완료\",F{first}:F{last})")),
				row("진행중 건수", "=COUNTIF(H{first}:H{last},\"진행중\")"),
			),
	),
	tmpl("asset-register", "자산 대장", "운영·재고", "취득가와 내용연수로 감가상각비와 장부가액을 계산합니다.",
		sheet("자산").tab("#65a30d").cols(110, 170, 110, 110, 130, 90, 90, 130, 130).
			title("자산 대장").note("정액법 기준입니다. 경과연수를 갱신하면 장부가액이 다시 계산됩니다.").
			head("자산번호", "자산명", "취득일", "부서", "취득가", "내용연수", "경과연수", "연 감가상각", "장부가액").
			rows(
				row("FA-001", "업무용 노트북 10대", "2024-03-15", "개발본부", 18000000, 4, 2, "=ROUND(E{r}/F{r},0)", "=MAX(E{r}-H{r}*G{r},0)"),
				row("FA-002", "회의실 디스플레이", "2023-06-01", "경영지원", 4200000, 5, 3, "=ROUND(E{r}/F{r},0)", "=MAX(E{r}-H{r}*G{r},0)"),
				row("FA-003", "물류 지게차", "2021-09-20", "물류팀", 32000000, 8, 5, "=ROUND(E{r}/F{r},0)", "=MAX(E{r}-H{r}*G{r},0)"),
				row("FA-004", "서버 랙 2식", "2025-01-10", "개발본부", 26500000, 5, 1, "=ROUND(E{r}/F{r},0)", "=MAX(E{r}-H{r}*G{r},0)"),
			).
			total("합계", nil, nil, nil, "=SUM(E{first}:E{last})", nil, nil, "=SUM(H{first}:H{last})", "=SUM(I{first}:I{last})").
			format(formatDate, 3).format(formatMoney, 5, 8, 9).format(formatNumber, 6, 7).
			summary(row("누적 감가상각", "=SUM(E{first}:E{last})-SUM(I{first}:I{last})")),
	),
	tmpl("vendor-list", "거래처 관리 대장", "운영·재고", "거래처 정보와 결제 조건, 거래 실적을 정리합니다.",
		sheet("거래처").tab("#65a30d").cols(150, 110, 130, 110, 110, 130, 110).
			title("거래처 관리 대장").note("결제 조건과 최근 거래일을 함께 관리하면 정산 누락을 막을 수 있습니다.").
			head("거래처", "담당자", "연락처", "구분", "결제 조건", "연 거래액", "최근 거래").
			rows(
				row("우성지류", "김현우", "031-455-8800", "매입", "월말 결제", 96000000, "2026-08-03"),
				row("한빛물류", "정유진", "031-777-2043", "매입", "익월 15일", 142000000, "2026-08-05"),
				row("성진산업", "장하늘", "053-244-7788", "매입", "선결제", 78500000, "2026-08-08"),
				row("가온테크", "김도현", "02-555-0182", "매출", "월말 결제", 210000000, "2026-08-01"),
				row("대현식품", "문서영", "062-812-3355", "매출", "익월 10일", 64000000, "2026-07-30"),
			).
			total("합계", nil, nil, nil, nil, "=SUM(F{first}:F{last})", nil).
			format(formatMoney, 6).format(formatDate, 7).
			summary(
				row("매입 거래액", won("=SUMIF(D{first}:D{last},\"매입\",F{first}:F{last})")),
				row("매출 거래액", won("=SUMIF(D{first}:D{last},\"매출\",F{first}:F{last})")),
			),
	),
	tmpl("quality-checklist", "품질 점검 체크리스트", "운영·재고", "점검 항목별 합격 여부와 합격률을 집계합니다.",
		sheet("점검").tab("#dc4f4f").cols(70, 220, 110, 110, 100, 170).
			title("품질 점검 체크리스트").note("판정에 합격 또는 불합격을 입력하면 합격률이 계산됩니다.").
			head("No", "점검 항목", "기준", "측정값", "판정", "조치 사항").
			rows(
				row(1, "외관 손상 여부", "손상 없음", "이상 없음", "합격", nil),
				row(2, "치수 오차", "±0.5mm 이내", "0.3mm", "합격", nil),
				row(3, "포장 상태", "밀봉 완전", "일부 개봉", "불합격", "재포장 후 재검사"),
				row(4, "라벨 표기", "규격 일치", "일치", "합격", nil),
				row(5, "동작 시험", "3회 연속 정상", "2회 정상", "불합격", "부품 교체 후 재시험"),
			).
			summary(
				row("합격", "=COUNTIF(E{first}:E{last},\"합격\")"),
				row("불합격", "=COUNTIF(E{first}:E{last},\"불합격\")"),
				row("합격률", pct("=IFERROR(COUNTIF(E{first}:E{last},\"합격\")/COUNTA(E{first}:E{last}),0)")),
			),
	),

	// 개인·기타 --------------------------------------------------------------
	tmpl("household-budget", "가계부", "개인·기타", "수입과 지출을 적으면 잔액과 분류별 지출이 정리됩니다.",
		sheet("가계부").tab("#ec4899").cols(110, 100, 170, 120, 120, 130).
			title("가계부").note("수입과 지출 중 해당하는 칸에만 금액을 적으면 잔액이 이어집니다.").
			head("일자", "분류", "내용", "수입", "지출", "잔액").
			rows(
				row("2026-08-01", "이월", "전월 잔액", 0, 0, 1850000),
				row("2026-08-05", "급여", "8월 급여", 3600000, 0, "=F{p}+D{r}-E{r}"),
				row("2026-08-06", "주거", "월세", 0, 750000, "=F{p}+D{r}-E{r}"),
				row("2026-08-08", "식비", "장보기", 0, 168000, "=F{p}+D{r}-E{r}"),
				row("2026-08-12", "교통", "교통카드 충전", 0, 60000, "=F{p}+D{r}-E{r}"),
				row("2026-08-17", "문화", "도서·영화", 0, 47000, "=F{p}+D{r}-E{r}"),
				row("2026-08-22", "저축", "적금 이체", 0, 500000, "=F{p}+D{r}-E{r}"),
			).
			total("합계", nil, nil, "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", "=F{last}").
			format(formatDate, 1).format(formatMoney, 4, 5, 6).
			summary(
				row("이번 달 저축", won("=SUMIF(B{first}:B{last},\"저축\",E{first}:E{last})")),
				row("식비 지출", won("=SUMIF(B{first}:B{last},\"식비\",E{first}:E{last})")),
				row("수입 대비 지출률", pct("=IFERROR(SUM(E{first}:E{last})/SUM(D{first}:D{last}),0)")),
			),
	),
	tmpl("weekly-planner", "주간 플래너", "개인·기타", "요일별 할 일과 소요 시간을 계획하고 완료를 표시합니다.",
		sheet("주간계획").tab("#3b82f6").cols(90, 200, 110, 100, 90, 90).
			title("주간 플래너").note("계획 시간과 실제 시간을 함께 적으면 주간 회고에 쓰기 좋습니다.").
			head("요일", "할 일", "분류", "계획(시간)", "실제", "완료").
			rows(
				row("월", "주간 계획 수립", "업무", 1.0, 1.0, "완료"),
				row("화", "제안서 초안 작성", "업무", 3.0, 3.5, "완료"),
				row("수", "운동", "건강", 1.0, 0.0, "미완료"),
				row("목", "고객 미팅 준비", "업무", 2.0, 2.0, "완료"),
				row("금", "주간 회고", "업무", 1.0, 0.5, "완료"),
				row("토", "독서", "자기계발", 2.0, 2.0, "완료"),
				row("일", "가족 시간", "생활", 4.0, 4.0, "완료"),
			).
			total("합계", nil, nil, "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", nil).
			format(formatDecimal, 4, 5).
			summary(
				row("완료율", pct("=IFERROR(COUNTIF(F{first}:F{last},\"완료\")/COUNTA(F{first}:F{last}),0)")),
				row("계획 대비 실행", "=IFERROR(SUM(E{first}:E{last})/SUM(D{first}:D{last}),0)"),
			),
	),
	tmpl("study-plan", "학습 계획표", "개인·기타", "과목별 학습 목표와 진도를 관리합니다.",
		sheet("학습계획").tab("#06b6d4").cols(120, 170, 110, 100, 100, 100).
			title("학습 계획표").note("목표 시간과 학습 시간을 입력하면 달성률이 계산됩니다.").
			head("과목", "학습 내용", "목표일", "목표(시간)", "학습(시간)", "달성률").
			rows(
				row("데이터 분석", "SQL 윈도우 함수", "2026-08-15", 10, 7, "=IFERROR(E{r}/D{r},0)"),
				row("데이터 분석", "통계 기초", "2026-08-31", 12, 4, "=IFERROR(E{r}/D{r},0)"),
				row("영어", "비즈니스 이메일", "2026-09-15", 8, 8, "=IFERROR(E{r}/D{r},0)"),
				row("클라우드", "네트워크 구성", "2026-09-30", 15, 3, "=IFERROR(E{r}/D{r},0)"),
			).
			total("합계", nil, nil, "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", "=IFERROR(SUM(E{first}:E{last})/SUM(D{first}:D{last}),0)").
			format(formatDate, 3).format(formatDecimal, 4, 5).format(formatPercent, 6).
			summary(row("완료 과정", "=COUNTIF(F{first}:F{last},\">=1\")")),
	),
	tmpl("event-planner", "행사 준비 체크리스트", "개인·기타", "행사 준비 항목과 예산 집행을 함께 관리합니다.",
		sheet("행사준비").tab("#8b5cf6").cols(70, 190, 110, 110, 120, 120, 90).
			title("행사 준비 체크리스트").note("예산과 실제 비용을 함께 적으면 남은 예산이 계산됩니다.").
			head("No", "준비 항목", "담당", "기한", "예산", "실제 비용", "상태").
			rows(
				row(1, "장소 예약", "정유진", "2026-08-20", 3000000, 2850000, "완료"),
				row(2, "초청장 발송", "최수아", "2026-08-25", 400000, 380000, "완료"),
				row(3, "케이터링 계약", "김도현", "2026-09-01", 2500000, 0, "진행중"),
				row(4, "발표 자료 취합", "박지민", "2026-09-05", 0, 0, "진행중"),
				row(5, "현장 리허설", "이서준", "2026-09-10", 300000, 0, "대기"),
			).
			total("합계", nil, nil, nil, "=SUM(E{first}:E{last})", "=SUM(F{first}:F{last})", nil).
			format(formatDate, 4).format(formatMoney, 5, 6).
			summary(
				row("남은 예산", won("=SUM(E{first}:E{last})-SUM(F{first}:F{last})")),
				row("완료 항목", "=COUNTIF(G{first}:G{last},\"완료\")"),
				row("진행률", pct("=IFERROR(COUNTIF(G{first}:G{last},\"완료\")/COUNTA(G{first}:G{last}),0)")),
			),
	),

	// 부동산 -----------------------------------------------------------------
	tmpl("real-estate-portfolio", "임대 부동산 포트폴리오", "부동산", "보유 부동산의 시세와 임대수입으로 수익률과 평가손익을 계산합니다.",
		sheet("포트폴리오").tab("#0891b2").cols(150, 100, 110, 130, 130, 120, 110, 100, 100).
			title("임대 부동산 포트폴리오").note("취득가와 현재 시세, 월 임대료만 입력하면 수익률과 평가손익이 계산됩니다.").
			head("물건명", "유형", "지역", "취득가", "현재 시세", "월 임대료", "연 임대수입", "평가손익", "수익률").
			rows(
				row("역삼 오피스텔", "오피스텔", "서울 강남", 320000000, 365000000, 1300000, "=F{r}*12", "=E{r}-D{r}", "=IFERROR(G{r}/E{r},0)"),
				row("부천 상가", "상가", "경기 부천", 480000000, 505000000, 2400000, "=F{r}*12", "=E{r}-D{r}", "=IFERROR(G{r}/E{r},0)"),
				row("수원 아파트", "아파트", "경기 수원", 610000000, 587000000, 1650000, "=F{r}*12", "=E{r}-D{r}", "=IFERROR(G{r}/E{r},0)"),
				row("대전 다가구", "다가구", "대전 유성", 850000000, 910000000, 4100000, "=F{r}*12", "=E{r}-D{r}", "=IFERROR(G{r}/E{r},0)"),
				row("제주 펜션", "숙박", "제주 서귀포", 430000000, 448000000, 2200000, "=F{r}*12", "=E{r}-D{r}", "=IFERROR(G{r}/E{r},0)"),
			).
			total("합계", nil, nil, "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", "=SUM(F{first}:F{last})", "=SUM(G{first}:G{last})", "=SUM(H{first}:H{last})", "=IFERROR(SUM(G{first}:G{last})/SUM(E{first}:E{last}),0)").
			format(formatMoney, 4, 5, 6, 7, 8).format(formatPercent, 9).
			summary(
				row("평균 수익률", pct("=ROUND(AVERAGE(I{first}:I{last}),4)")),
				row("최고 수익 물건", "=INDEX(A{first}:A{last},MATCH(MAX(I{first}:I{last}),I{first}:I{last},0))"),
				row("평가이익 물건 수", num("=COUNTIF(H{first}:H{last},\">0\")")),
			),
	),
	tmpl("rent-roll", "임대료 관리대장", "부동산", "호실별 임차인과 보증금·월세, 연체 현황을 한 장으로 관리합니다.",
		sheet("렌트롤").tab("#0891b2").cols(80, 130, 110, 130, 120, 110, 110, 100, 100).
			title("임대료 관리대장 (렌트롤)").note("납부한 개월과 계약 개월을 입력하면 미수금과 납부율이 계산됩니다.").
			head("호실", "임차인", "업종", "보증금", "월 임대료", "계약 개월", "납부 개월", "미수금", "납부율").
			rows(
				row("101", "가온커피", "카페", 30000000, 1800000, 12, 12, "=(F{r}-G{r})*E{r}", "=IFERROR(G{r}/F{r},0)"),
				row("102", "한빛문구", "소매", 20000000, 1200000, 12, 11, "=(F{r}-G{r})*E{r}", "=IFERROR(G{r}/F{r},0)"),
				row("201", "미래학원", "교육", 50000000, 2600000, 24, 22, "=(F{r}-G{r})*E{r}", "=IFERROR(G{r}/F{r},0)"),
				row("202", "성진세무", "사무", 40000000, 2100000, 24, 24, "=(F{r}-G{r})*E{r}", "=IFERROR(G{r}/F{r},0)"),
				row("301", "공실", "-", 0, 0, 0, 0, "=(F{r}-G{r})*E{r}", "=IFERROR(G{r}/F{r},0)"),
			).
			total("합계", nil, nil, "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", nil, nil, "=SUM(H{first}:H{last})", "=IFERROR(SUM(G{first}:G{last})/SUM(F{first}:F{last}),0)").
			format(formatMoney, 4, 5, 8).format(formatNumber, 6, 7).format(formatPercent, 9).
			summary(
				row("공실 호실 수", num("=COUNTIF(B{first}:B{last},\"공실\")")),
				row("월 임대수입", won("=SUM(E{first}:E{last})")),
				row("미수 총액", won("=SUM(H{first}:H{last})")),
			),
	),
	tmpl("mortgage-schedule", "주택담보대출 상환 스케줄", "부동산", "원리금 균등 상환의 회차별 이자와 원금, 잔액을 계산합니다.",
		sheet("상환표").tab("#5268a6").cols(70, 130, 130, 130, 140, 110).
			title("주택담보대출 상환 스케줄").note("1회차의 대출 잔액과 연이율, 총 회차를 넣으면 이후 회차가 이어집니다.").
			head("회차", "기초 잔액", "월 상환액", "이자", "원금", "기말 잔액").
			rows(
				row(1, 300000000, "=ROUND(300000000*(0.045/12)/(1-POWER(1+0.045/12,-120)),0)", "=ROUND(B{r}*0.045/12,0)", "=C{r}-D{r}", "=B{r}-E{r}"),
				row(2, "=F{p}", "=C{p}", "=ROUND(B{r}*0.045/12,0)", "=C{r}-D{r}", "=B{r}-E{r}"),
				row(3, "=F{p}", "=C{p}", "=ROUND(B{r}*0.045/12,0)", "=C{r}-D{r}", "=B{r}-E{r}"),
				row(4, "=F{p}", "=C{p}", "=ROUND(B{r}*0.045/12,0)", "=C{r}-D{r}", "=B{r}-E{r}"),
				row(5, "=F{p}", "=C{p}", "=ROUND(B{r}*0.045/12,0)", "=C{r}-D{r}", "=B{r}-E{r}"),
				row(6, "=F{p}", "=C{p}", "=ROUND(B{r}*0.045/12,0)", "=C{r}-D{r}", "=B{r}-E{r}"),
			).
			total("6회차 누계", nil, "=SUM(C{first}:C{last})", "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", "=F{last}").
			format(formatNumber, 1).format(formatMoney, 2, 3, 4, 5, 6).
			summary(
				row("연이율", pct(0.045)),
				row("총 회차", num(120)),
				row("월 상환액", won("=C{first}")),
				row("납입 이자 비중", pct("=IFERROR(SUM(D{first}:D{last})/SUM(C{first}:C{last}),0)")),
			),
	),
	tmpl("jeonse-vs-monthly", "전세 · 월세 비교", "부동산", "전월세 전환율과 보증금 기회비용까지 넣어 어느 쪽이 유리한지 비교합니다.",
		sheet("비교").tab("#5268a6").cols(170, 150, 150, 130).
			title("전세와 월세 비교").note("보증금은 예금 이자만큼 기회비용이 생긴다고 보고 연간 총비용을 비교합니다.").
			head("항목", "전세", "월세", "차이").
			rows(
				row("보증금", 400000000, 50000000, "=B{r}-C{r}"),
				row("월 임대료", 0, 1500000, "=B{r}-C{r}"),
				row("연 임대료", "=B{p}*12", "=C{p}*12", "=B{r}-C{r}"),
				row("예금 이자율", pct(0.035), pct(0.035), pct(0)),
				row("보증금 기회비용", "=ROUND(B4*B7,0)", "=ROUND(C4*C7,0)", "=B{r}-C{r}"),
				row("연 대출이자", 6000000, 0, "=B{r}-C{r}"),
			).
			total("연간 총비용", "=B6+B8+B9", "=C6+C8+C9", "=B{r}-C{r}").
			format(formatMoney, 2, 3, 4).
			summary(
				row("유리한 선택", "=IF(B10<C10,\"전세\",IF(B10>C10,\"월세\",\"동일\"))"),
				row("연간 절감액", won("=ABS(B10-C10)")),
				row("전월세 전환율", pct("=IFERROR((C5*12)/(B4-C4),0)")),
			),
	),
	tmpl("property-purchase-cost", "부동산 취득 비용 계산", "부동산", "취득세와 중개보수, 법무비까지 더한 실제 매입 총액을 계산합니다.",
		sheet("취득비용").tab("#22c55e").cols(170, 120, 150, 190).
			title("부동산 취득 비용").note("매매가와 요율을 입력하면 항목별 금액과 총 매입비용이 계산됩니다.").
			head("항목", "요율", "금액", "비고").
			rows(
				row("매매가", nil, 650000000, "계약서 기준"),
				row("취득세", 0.011, "=ROUND(C4*B{r},0)", "1주택 85㎡ 이하 기준"),
				row("지방교육세", 0.001, "=ROUND(C4*B{r},0)", "취득세 부가"),
				row("중개보수", 0.004, "=ROUND(C4*B{r},0)", "상한 요율"),
				row("법무사 비용", nil, 750000, "등기 대행"),
				row("인지세·채권", nil, 320000, "국민주택채권 할인 포함"),
				row("이사·수리비", nil, 4500000, "예상"),
			).
			total("총 매입비용", nil, "=SUM(C{first}:C{last})", nil).
			format(formatPercent, 2).format(formatMoney, 3).
			summary(
				row("부대비용 합계", won("=SUM(C5:C{last})")),
				row("매매가 대비 부대비용", pct("=IFERROR(SUM(C5:C{last})/C4,0)")),
			),
	),
	tmpl("rental-yield", "임대 수익률 분석", "부동산", "표면 수익률과 무차입 실질 수익률, 자기자본 수익률을 나눠 계산합니다.",
		sheet("수익률").tab("#22c55e").cols(190, 150, 230).
			title("임대 수익률 분석").note("매입 조건과 대출, 임대 조건을 넣으면 레버리지 효과까지 판단합니다.").
			head("항목", "값", "설명").
			rows(
				row("매입가", 520000000, "부대비용 제외"),
				row("부대비용", 28000000, "취득세·중개보수 등"),
				row("총 투자금", "=B4+B5", "매입가 + 부대비용"),
				row("대출금", 300000000, "담보대출"),
				row("자기자본", "=B6-B7", "총 투자금 - 대출금"),
				row("보증금", 30000000, "임차인 보증금"),
				row("실투자금", "=B8-B9", "자기자본 - 보증금"),
				row("월 임대료", 1900000, "관리비 제외"),
				row("연 임대수입", "=B11*12", "월 임대료 × 12"),
				row("연 보유비용", 2400000, "재산세·관리·수선"),
				row("연 순영업수익(NOI)", "=B12-B13", "임대수입 - 보유비용"),
				row("연 대출이자", "=ROUND(B7*0.048,0)", "연이율 4.8%"),
				row("세전 현금흐름", "=B14-B15", "NOI - 이자"),
			).
			format(formatMoney, 2).
			summary(
				row("표면 수익률", pct("=IFERROR(B12/B4,0)")),
				row("실질 수익률 (무차입)", pct("=IFERROR(B14/B6,0)")),
				row("자기자본 수익률", pct("=IFERROR(B16/B10,0)")),
				row("레버리지 효과", "=IF(IFERROR(B16/B10,0)>IFERROR(B14/B6,0),\"플러스\",\"마이너스\")"),
				row("손익분기 이자율", pct("=IFERROR(B14/B6,0)")),
			),
	),
	tmpl("apartment-comparison", "아파트 매물 비교", "부동산", "평단가와 항목별 점수를 가중 합산해 매물 순위를 매깁니다.",
		sheet("매물비교").tab("#3b82f6").cols(150, 110, 90, 130, 120, 80, 80, 80, 90).
			title("아파트 매물 비교").note("입지·학군·교통 점수는 1~5로 입력하면 가중 점수와 순위가 계산됩니다.").
			head("단지", "지역", "전용(㎡)", "매매가", "평단가", "입지", "학군", "교통", "종합점수").
			rows(
				row("가온파크", "수원 영통", 84.9, 720000000, "=ROUND(D{r}/(C{r}/3.3058),0)", 4, 5, 4, "=ROUND(F{r}*0.4+G{r}*0.35+H{r}*0.25,2)"),
				row("한빛리버", "성남 분당", 59.8, 810000000, "=ROUND(D{r}/(C{r}/3.3058),0)", 5, 4, 5, "=ROUND(F{r}*0.4+G{r}*0.35+H{r}*0.25,2)"),
				row("미래스카이", "용인 수지", 101.2, 690000000, "=ROUND(D{r}/(C{r}/3.3058),0)", 3, 4, 3, "=ROUND(F{r}*0.4+G{r}*0.35+H{r}*0.25,2)"),
				row("성진뷰", "화성 동탄", 74.5, 560000000, "=ROUND(D{r}/(C{r}/3.3058),0)", 4, 3, 4, "=ROUND(F{r}*0.4+G{r}*0.35+H{r}*0.25,2)"),
			).
			format(formatDecimal, 3, 9).format(formatMoney, 4, 5).format(formatNumber, 6, 7, 8).
			summary(
				row("최고 점수 단지", "=INDEX(A{first}:A{last},MATCH(MAX(I{first}:I{last}),I{first}:I{last},0))"),
				row("최저 평단가 단지", "=INDEX(A{first}:A{last},MATCH(MIN(E{first}:E{last}),E{first}:E{last},0))"),
				row("평균 평단가", won("=ROUND(AVERAGE(E{first}:E{last}),0)")),
			),
	),
	tmpl("real-estate-cashflow", "부동산 현금흐름 추정", "부동산", "연차별 임대수입과 비용, 누적 현금흐름을 추정합니다.",
		sheet("현금흐름").tab("#3b82f6").cols(80, 130, 120, 120, 130, 140).
			title("부동산 5년 현금흐름").note("임대료 상승률을 반영해 연차별 순현금흐름과 누적액을 봅니다.").
			head("연차", "임대수입", "공실손실", "운영비용", "순현금흐름", "누적 현금흐름").
			rows(
				row(1, 22800000, "=ROUND(B{r}*0.05,0)", 4200000, "=B{r}-C{r}-D{r}", "=E{r}"),
				row(2, "=ROUND(B{p}*1.03,0)", "=ROUND(B{r}*0.05,0)", 4300000, "=B{r}-C{r}-D{r}", "=F{p}+E{r}"),
				row(3, "=ROUND(B{p}*1.03,0)", "=ROUND(B{r}*0.05,0)", 4400000, "=B{r}-C{r}-D{r}", "=F{p}+E{r}"),
				row(4, "=ROUND(B{p}*1.03,0)", "=ROUND(B{r}*0.05,0)", 4600000, "=B{r}-C{r}-D{r}", "=F{p}+E{r}"),
				row(5, "=ROUND(B{p}*1.03,0)", "=ROUND(B{r}*0.05,0)", 4800000, "=B{r}-C{r}-D{r}", "=F{p}+E{r}"),
			).
			total("합계", "=SUM(B{first}:B{last})", "=SUM(C{first}:C{last})", "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", "=F{last}").
			format(formatNumber, 1).format(formatMoney, 2, 3, 4, 5, 6).
			summary(
				row("연평균 순현금흐름", won("=ROUND(AVERAGE(E{first}:E{last}),0)")),
				row("5년 누적", won("=F{last}")),
			),
	),
	tmpl("building-maintenance", "건물 관리비 정산", "부동산", "호실별 면적 비율로 공용 관리비를 배분합니다.",
		sheet("관리비").tab("#65a30d").cols(80, 130, 100, 110, 130, 130, 130).
			title("건물 관리비 정산").note("공용 비용 합계를 전용면적 비율로 나눠 호실별 청구액을 만듭니다.").
			head("호실", "임차인", "전용면적", "면적 비율", "공용관리비", "개별 사용료", "청구 합계").
			rows(
				row("101", "가온커피", 82.5, "=IFERROR(C{r}/SUM(C{first}:C{last}),0)", "=ROUND(D{r}*4800000,0)", 320000, "=E{r}+F{r}"),
				row("102", "한빛문구", 64.0, "=IFERROR(C{r}/SUM(C{first}:C{last}),0)", "=ROUND(D{r}*4800000,0)", 180000, "=E{r}+F{r}"),
				row("201", "미래학원", 128.7, "=IFERROR(C{r}/SUM(C{first}:C{last}),0)", "=ROUND(D{r}*4800000,0)", 540000, "=E{r}+F{r}"),
				row("202", "성진세무", 96.3, "=IFERROR(C{r}/SUM(C{first}:C{last}),0)", "=ROUND(D{r}*4800000,0)", 260000, "=E{r}+F{r}"),
			).
			total("합계", nil, "=SUM(C{first}:C{last})", "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", "=SUM(F{first}:F{last})", "=SUM(G{first}:G{last})").
			format(formatDecimal, 3).format(formatPercent, 4).format(formatMoney, 5, 6, 7).
			summary(row("공용비 총액", won(4800000))),
	),
	tmpl("lease-expiry", "임대차 만기 관리", "부동산", "계약 만기와 갱신 여부, 인상 예정액을 관리합니다.",
		sheet("계약만기").tab("#65a30d").cols(80, 130, 110, 110, 120, 120, 110, 100).
			title("임대차 만기 관리").note("갱신 여부와 인상률을 입력하면 갱신 후 임대료가 계산됩니다.").
			head("호실", "임차인", "계약 시작", "계약 만기", "현재 임대료", "인상률", "갱신 후", "상태").
			rows(
				row("101", "가온커피", "2024-03-01", "2026-02-28", 1800000, 0.05, "=ROUND(E{r}*(1+F{r}),0)", "갱신 협의"),
				row("102", "한빛문구", "2025-01-15", "2027-01-14", 1200000, 0.03, "=ROUND(E{r}*(1+F{r}),0)", "유지"),
				row("201", "미래학원", "2023-09-01", "2026-08-31", 2600000, 0.04, "=ROUND(E{r}*(1+F{r}),0)", "갱신 확정"),
				row("202", "성진세무", "2024-11-01", "2026-10-31", 2100000, 0.0, "=ROUND(E{r}*(1+F{r}),0)", "퇴거 예정"),
			).
			total("합계", nil, nil, nil, "=SUM(E{first}:E{last})", nil, "=SUM(G{first}:G{last})", nil).
			format(formatDate, 3, 4).format(formatMoney, 5, 7).format(formatPercent, 6).
			summary(
				row("퇴거 예정 호실", num("=COUNTIF(H{first}:H{last},\"퇴거 예정\")")),
				row("갱신 후 월 수입 증가", won("=SUM(G{first}:G{last})-SUM(E{first}:E{last})")),
			),
	),

	// 주식·투자 ---------------------------------------------------------------
	tmpl("stock-portfolio", "주식 포트폴리오", "주식·투자", "보유 종목의 평가손익과 비중, 목표가 괴리율을 계산합니다.",
		sheet("포트폴리오").tab("#dc4f4f").cols(120, 90, 90, 110, 110, 130, 130, 110, 90, 100).
			title("주식 포트폴리오").note("수량과 평균 매입가, 현재가만 입력하면 손익과 비중이 계산됩니다.").
			head("종목", "코드", "수량", "평균단가", "현재가", "매입금액", "평가금액", "평가손익", "수익률", "비중").
			rows(
				row("삼성전자", "005930", 120, 68500, 74300, "=C{r}*D{r}", "=C{r}*E{r}", "=G{r}-F{r}", "=IFERROR(H{r}/F{r},0)", "=IFERROR(G{r}/SUM(G{first}:G{last}),0)"),
				row("SK하이닉스", "000660", 30, 152000, 187500, "=C{r}*D{r}", "=C{r}*E{r}", "=G{r}-F{r}", "=IFERROR(H{r}/F{r},0)", "=IFERROR(G{r}/SUM(G{first}:G{last}),0)"),
				row("현대차", "005380", 45, 213000, 198500, "=C{r}*D{r}", "=C{r}*E{r}", "=G{r}-F{r}", "=IFERROR(H{r}/F{r},0)", "=IFERROR(G{r}/SUM(G{first}:G{last}),0)"),
				row("카카오", "035720", 80, 57400, 43200, "=C{r}*D{r}", "=C{r}*E{r}", "=G{r}-F{r}", "=IFERROR(H{r}/F{r},0)", "=IFERROR(G{r}/SUM(G{first}:G{last}),0)"),
				row("KODEX 200", "069500", 200, 34100, 36850, "=C{r}*D{r}", "=C{r}*E{r}", "=G{r}-F{r}", "=IFERROR(H{r}/F{r},0)", "=IFERROR(G{r}/SUM(G{first}:G{last}),0)"),
			).
			total("합계", nil, nil, nil, nil, "=SUM(F{first}:F{last})", "=SUM(G{first}:G{last})", "=SUM(H{first}:H{last})", "=IFERROR(SUM(H{first}:H{last})/SUM(F{first}:F{last}),0)", 1).
			format(formatNumber, 3).format(formatMoney, 4, 5, 6, 7, 8).format(formatPercent, 9, 10).
			summary(
				row("수익 종목 수", num("=COUNTIF(H{first}:H{last},\">0\")")),
				row("최대 수익 종목", "=INDEX(A{first}:A{last},MATCH(MAX(I{first}:I{last}),I{first}:I{last},0))"),
				row("최대 손실 종목", "=INDEX(A{first}:A{last},MATCH(MIN(I{first}:I{last}),I{first}:I{last},0))"),
			),
	),
	tmpl("dividend-tracker", "배당금 관리", "주식·투자", "종목별 배당금과 배당수익률, 세후 실수령액을 계산합니다.",
		sheet("배당").tab("#dc4f4f").cols(120, 90, 110, 130, 110, 130, 120, 130, 110).
			title("배당금 관리").note("주당 배당금과 보유 수량을 입력하면 세후 배당금과 수익률이 계산됩니다.").
			head("종목", "수량", "매입단가", "매입금액", "주당 배당금", "배당금", "배당소득세", "세후 배당금", "배당수익률").
			rows(
				row("삼성전자", 120, 68500, "=B{r}*C{r}", 1444, "=B{r}*E{r}", "=ROUND(F{r}*0.154,0)", "=F{r}-G{r}", "=IFERROR(E{r}/C{r},0)"),
				row("KT&G", 40, 88900, "=B{r}*C{r}", 5200, "=B{r}*E{r}", "=ROUND(F{r}*0.154,0)", "=F{r}-G{r}", "=IFERROR(E{r}/C{r},0)"),
				row("맥쿼리인프라", 300, 12150, "=B{r}*C{r}", 770, "=B{r}*E{r}", "=ROUND(F{r}*0.154,0)", "=F{r}-G{r}", "=IFERROR(E{r}/C{r},0)"),
				row("삼성화재", 25, 312000, "=B{r}*C{r}", 16000, "=B{r}*E{r}", "=ROUND(F{r}*0.154,0)", "=F{r}-G{r}", "=IFERROR(E{r}/C{r},0)"),
			).
			total("합계", nil, nil, "=SUM(D{first}:D{last})", nil, "=SUM(F{first}:F{last})", "=SUM(G{first}:G{last})", "=SUM(H{first}:H{last})", "=IFERROR(SUM(F{first}:F{last})/SUM(D{first}:D{last}),0)").
			format(formatNumber, 2).format(formatMoney, 3, 4, 5, 6, 7, 8).format(formatPercent, 9).
			summary(
				row("월 평균 세후 배당", won("=ROUND(SUM(H{first}:H{last})/12,0)")),
				row("포트폴리오 배당수익률", pct("=IFERROR(SUM(F{first}:F{last})/SUM(D{first}:D{last}),0)")),
				row("배당소득세 합계", won("=SUM(G{first}:G{last})")),
			),
	),
	tmpl("trade-journal", "매매일지", "주식·투자", "매매 기록에서 손익과 승률, 손익비를 계산합니다.",
		sheet("매매일지").tab("#f59e0b").cols(110, 110, 80, 90, 110, 110, 120, 100, 90).
			title("매매일지").note("매수·매도 가격과 수량을 적으면 수수료를 반영한 실현손익이 계산됩니다.").
			head("매도일", "종목", "수량", "매수가", "매도가", "매매비용", "실현손익", "수익률", "결과").
			rows(
				row("2026-07-03", "삼성전자", 50, 66200, 71500, "=ROUND((C{r}*D{r}+C{r}*E{r})*0.0015,0)", "=C{r}*(E{r}-D{r})-F{r}", "=IFERROR(G{r}/(C{r}*D{r}),0)", "=IF(G{r}>0,\"이익\",\"손실\")"),
				row("2026-07-11", "카카오", 100, 58900, 54300, "=ROUND((C{r}*D{r}+C{r}*E{r})*0.0015,0)", "=C{r}*(E{r}-D{r})-F{r}", "=IFERROR(G{r}/(C{r}*D{r}),0)", "=IF(G{r}>0,\"이익\",\"손실\")"),
				row("2026-07-24", "SK하이닉스", 20, 168000, 192000, "=ROUND((C{r}*D{r}+C{r}*E{r})*0.0015,0)", "=C{r}*(E{r}-D{r})-F{r}", "=IFERROR(G{r}/(C{r}*D{r}),0)", "=IF(G{r}>0,\"이익\",\"손실\")"),
				row("2026-08-05", "현대차", 30, 219000, 203500, "=ROUND((C{r}*D{r}+C{r}*E{r})*0.0015,0)", "=C{r}*(E{r}-D{r})-F{r}", "=IFERROR(G{r}/(C{r}*D{r}),0)", "=IF(G{r}>0,\"이익\",\"손실\")"),
				row("2026-08-12", "NAVER", 40, 178500, 191000, "=ROUND((C{r}*D{r}+C{r}*E{r})*0.0015,0)", "=C{r}*(E{r}-D{r})-F{r}", "=IFERROR(G{r}/(C{r}*D{r}),0)", "=IF(G{r}>0,\"이익\",\"손실\")"),
			).
			total("합계", nil, nil, nil, nil, "=SUM(F{first}:F{last})", "=SUM(G{first}:G{last})", nil, nil).
			format(formatDate, 1).format(formatNumber, 3).format(formatMoney, 4, 5, 6, 7).format(formatPercent, 8).
			summary(
				row("승률", pct("=IFERROR(COUNTIF(I{first}:I{last},\"이익\")/COUNTA(I{first}:I{last}),0)")),
				row("평균 이익", won("=IFERROR(ROUND(SUMIF(I{first}:I{last},\"이익\",G{first}:G{last})/COUNTIF(I{first}:I{last},\"이익\"),0),0)")),
				row("평균 손실", won("=IFERROR(ROUND(SUMIF(I{first}:I{last},\"손실\",G{first}:G{last})/COUNTIF(I{first}:I{last},\"손실\"),0),0)")),
				row("총 실현손익", won("=SUM(G{first}:G{last})")),
			),
	),
	tmpl("dca-plan", "적립식 투자 계획", "주식·투자", "매월 같은 금액을 넣었을 때 복리로 얼마가 되는지 계산합니다.",
		sheet("적립계획").tab("#22c55e").cols(80, 130, 140, 140, 140).
			title("적립식 투자 계획").note("연 수익률을 바꾸면 연차별 평가금액이 다시 계산됩니다.").
			head("연차", "연간 납입", "누적 납입", "연말 평가금액", "누적 수익").
			rows(
				row(1, 12000000, "=B{r}", "=ROUND(B{r}*POWER(1+0.07,0.5),0)", "=D{r}-C{r}"),
				row(2, 12000000, "=C{p}+B{r}", "=ROUND((D{p}+B{r})*POWER(1+0.07,1),0)", "=D{r}-C{r}"),
				row(3, 12000000, "=C{p}+B{r}", "=ROUND((D{p}+B{r})*POWER(1+0.07,1),0)", "=D{r}-C{r}"),
				row(4, 12000000, "=C{p}+B{r}", "=ROUND((D{p}+B{r})*POWER(1+0.07,1),0)", "=D{r}-C{r}"),
				row(5, 12000000, "=C{p}+B{r}", "=ROUND((D{p}+B{r})*POWER(1+0.07,1),0)", "=D{r}-C{r}"),
				row(10, 12000000, "=C{p}+B{r}*5", "=ROUND((D{p}+B{r}*5)*POWER(1+0.07,5),0)", "=D{r}-C{r}"),
			).
			format(formatNumber, 1).format(formatMoney, 2, 3, 4, 5).
			summary(
				row("가정 연 수익률", pct(0.07)),
				row("10년 후 평가금액", won("=D{last}")),
				row("10년 누적 수익률", pct("=IFERROR(E{last}/C{last},0)")),
			),
	),
	tmpl("stock-valuation", "기업가치 상대평가", "주식·투자", "PER과 PBR, ROE로 종목을 비교하고 적정주가를 추정합니다.",
		sheet("상대평가").tab("#8b5cf6").cols(120, 110, 110, 110, 90, 90, 90, 130, 110).
			title("기업가치 상대평가").note("EPS와 BPS, 현재가를 입력하면 PER·PBR과 업종 평균 기준 적정주가가 계산됩니다.").
			head("종목", "현재가", "EPS", "BPS", "PER", "PBR", "ROE", "적정주가(PER)", "괴리율").
			rows(
				row("가온전자", 74300, 6820, 58400, "=IFERROR(ROUND(B{r}/C{r},2),0)", "=IFERROR(ROUND(B{r}/D{r},2),0)", "=IFERROR(C{r}/D{r},0)", "=ROUND(C{r}*12,0)", "=IFERROR(H{r}/B{r}-1,0)"),
				row("한빛반도체", 187500, 11200, 96300, "=IFERROR(ROUND(B{r}/C{r},2),0)", "=IFERROR(ROUND(B{r}/D{r},2),0)", "=IFERROR(C{r}/D{r},0)", "=ROUND(C{r}*12,0)", "=IFERROR(H{r}/B{r}-1,0)"),
				row("미래모터스", 198500, 21400, 245000, "=IFERROR(ROUND(B{r}/C{r},2),0)", "=IFERROR(ROUND(B{r}/D{r},2),0)", "=IFERROR(C{r}/D{r},0)", "=ROUND(C{r}*12,0)", "=IFERROR(H{r}/B{r}-1,0)"),
				row("성진화학", 43200, 2150, 51200, "=IFERROR(ROUND(B{r}/C{r},2),0)", "=IFERROR(ROUND(B{r}/D{r},2),0)", "=IFERROR(C{r}/D{r},0)", "=ROUND(C{r}*12,0)", "=IFERROR(H{r}/B{r}-1,0)"),
			).
			format(formatMoney, 2, 3, 4, 8).format(formatDecimal, 5, 6).format(formatPercent, 7, 9).
			summary(
				row("업종 평균 PER", dec("=ROUND(AVERAGE(E{first}:E{last}),2)")),
				row("가장 저평가 종목", "=INDEX(A{first}:A{last},MATCH(MAX(I{first}:I{last}),I{first}:I{last},0))"),
				row("평균 ROE", pct("=ROUND(AVERAGE(G{first}:G{last}),4)")),
			),
	),
	tmpl("dcf-valuation", "DCF 기업가치 평가", "주식·투자", "연차별 잉여현금흐름을 할인해 기업가치를 계산합니다.",
		sheet("DCF").tab("#8b5cf6").cols(80, 140, 100, 120, 150, 150).
			title("DCF 기업가치 평가").note("연차별 FCF와 할인율을 입력하면 현재가치와 누적 기업가치가 계산됩니다.").
			head("연차", "잉여현금흐름", "할인율", "할인계수", "현재가치", "누적 현재가치").
			rows(
				row(1, 12000000000, 0.09, "=ROUND(1/POWER(1+C{r},A{r}),4)", "=ROUND(B{r}*D{r},0)", "=E{r}"),
				row(2, 13200000000, 0.09, "=ROUND(1/POWER(1+C{r},A{r}),4)", "=ROUND(B{r}*D{r},0)", "=F{p}+E{r}"),
				row(3, 14300000000, 0.09, "=ROUND(1/POWER(1+C{r},A{r}),4)", "=ROUND(B{r}*D{r},0)", "=F{p}+E{r}"),
				row(4, 15100000000, 0.09, "=ROUND(1/POWER(1+C{r},A{r}),4)", "=ROUND(B{r}*D{r},0)", "=F{p}+E{r}"),
				row(5, 15800000000, 0.09, "=ROUND(1/POWER(1+C{r},A{r}),4)", "=ROUND(B{r}*D{r},0)", "=F{p}+E{r}"),
			).
			total("합계", "=SUM(B{first}:B{last})", nil, nil, "=SUM(E{first}:E{last})", "=F{last}").
			format(formatNumber, 1).format(formatMoney, 2, 5, 6).format(formatPercent, 3).format(formatDecimal, 4).
			summary(
				row("영구성장률", pct(0.02)),
				row("잔존가치", won("=ROUND(B{last}*(1+0.02)/(0.09-0.02)*D{last},0)")),
				row("기업가치 합계", won("=F{last}+ROUND(B{last}*(1+0.02)/(0.09-0.02)*D{last},0)")),
			),
	),
	tmpl("asset-allocation", "자산 배분 리밸런싱", "주식·투자", "목표 비중과 현재 비중을 비교해 매수·매도 금액을 계산합니다.",
		sheet("리밸런싱").tab("#3b82f6").cols(140, 140, 100, 100, 100, 140, 110).
			title("자산 배분 리밸런싱").note("목표 비중을 정하면 현재 평가액과 비교해 조정 금액이 계산됩니다.").
			head("자산군", "현재 평가액", "현재 비중", "목표 비중", "차이", "조정 금액", "실행").
			rows(
				row("국내 주식", 32000000, "=IFERROR(B{r}/SUM(B{first}:B{last}),0)", 0.30, "=D{r}-C{r}", "=ROUND(E{r}*SUM(B{first}:B{last}),0)", "=IF(F{r}>0,\"매수\",IF(F{r}<0,\"매도\",\"유지\"))"),
				row("해외 주식", 41000000, "=IFERROR(B{r}/SUM(B{first}:B{last}),0)", 0.35, "=D{r}-C{r}", "=ROUND(E{r}*SUM(B{first}:B{last}),0)", "=IF(F{r}>0,\"매수\",IF(F{r}<0,\"매도\",\"유지\"))"),
				row("채권", 18000000, "=IFERROR(B{r}/SUM(B{first}:B{last}),0)", 0.20, "=D{r}-C{r}", "=ROUND(E{r}*SUM(B{first}:B{last}),0)", "=IF(F{r}>0,\"매수\",IF(F{r}<0,\"매도\",\"유지\"))"),
				row("현금성", 9000000, "=IFERROR(B{r}/SUM(B{first}:B{last}),0)", 0.10, "=D{r}-C{r}", "=ROUND(E{r}*SUM(B{first}:B{last}),0)", "=IF(F{r}>0,\"매수\",IF(F{r}<0,\"매도\",\"유지\"))"),
				row("금·원자재", 5000000, "=IFERROR(B{r}/SUM(B{first}:B{last}),0)", 0.05, "=D{r}-C{r}", "=ROUND(E{r}*SUM(B{first}:B{last}),0)", "=IF(F{r}>0,\"매수\",IF(F{r}<0,\"매도\",\"유지\"))"),
			).
			total("합계", "=SUM(B{first}:B{last})", 1, "=SUM(D{first}:D{last})", nil, nil, nil).
			format(formatMoney, 2, 6).format(formatPercent, 3, 4, 5).
			summary(
				row("매수 필요액", won("=SUMIF(F{first}:F{last},\">0\",F{first}:F{last})")),
				row("매도 필요액", won("=ABS(SUMIF(F{first}:F{last},\"<0\",F{first}:F{last}))")),
			),
	),
	tmpl("stock-watchlist", "관심종목 모니터링", "주식·투자", "목표가와 손절가 대비 현재가 위치를 한눈에 봅니다.",
		sheet("관심종목").tab("#06b6d4").cols(120, 90, 110, 110, 110, 110, 110, 110).
			title("관심종목 모니터링").note("목표가와 손절가를 입력하면 괴리율과 신호가 계산됩니다.").
			head("종목", "코드", "현재가", "목표가", "손절가", "목표 괴리율", "손절 여유", "신호").
			rows(
				row("가온전자", "005930", 74300, 88000, 66000, "=IFERROR(D{r}/C{r}-1,0)", "=IFERROR(C{r}/E{r}-1,0)", "=IF(C{r}<=E{r},\"손절\",IF(C{r}>=D{r},\"목표 도달\",\"보유\"))"),
				row("한빛반도체", "000660", 187500, 210000, 165000, "=IFERROR(D{r}/C{r}-1,0)", "=IFERROR(C{r}/E{r}-1,0)", "=IF(C{r}<=E{r},\"손절\",IF(C{r}>=D{r},\"목표 도달\",\"보유\"))"),
				row("미래모터스", "005380", 198500, 195000, 180000, "=IFERROR(D{r}/C{r}-1,0)", "=IFERROR(C{r}/E{r}-1,0)", "=IF(C{r}<=E{r},\"손절\",IF(C{r}>=D{r},\"목표 도달\",\"보유\"))"),
				row("성진화학", "035720", 43200, 62000, 45000, "=IFERROR(D{r}/C{r}-1,0)", "=IFERROR(C{r}/E{r}-1,0)", "=IF(C{r}<=E{r},\"손절\",IF(C{r}>=D{r},\"목표 도달\",\"보유\"))"),
			).
			format(formatMoney, 3, 4, 5).format(formatPercent, 6, 7).
			summary(
				row("손절 신호", num("=COUNTIF(H{first}:H{last},\"손절\")")),
				row("목표 도달", num("=COUNTIF(H{first}:H{last},\"목표 도달\")")),
				row("평균 목표 괴리율", pct("=ROUND(AVERAGE(F{first}:F{last}),4)")),
			),
	),
	tmpl("etf-comparison", "ETF 비교", "주식·투자", "보수와 추적오차, 거래대금을 비교해 ETF를 고릅니다.",
		sheet("ETF비교").tab("#06b6d4").cols(160, 110, 100, 100, 110, 130, 110).
			title("ETF 비교").note("총보수와 추적오차, 일평균 거래대금으로 종합 점수를 매깁니다.").
			head("ETF", "기초지수", "총보수", "추적오차", "1년 수익률", "일평균 거래대금", "종합점수").
			rows(
				row("KODEX 200", "KOSPI200", 0.0015, 0.0012, 0.084, 82000000000, "=ROUND((1-C{r}*100)*0.4+(1-D{r}*100)*0.3+E{r}*100*0.3,2)"),
				row("TIGER 미국S&P500", "S&P500", 0.0007, 0.0009, 0.152, 61000000000, "=ROUND((1-C{r}*100)*0.4+(1-D{r}*100)*0.3+E{r}*100*0.3,2)"),
				row("KBSTAR 종합채권", "KAP채권", 0.0012, 0.0006, 0.032, 9400000000, "=ROUND((1-C{r}*100)*0.4+(1-D{r}*100)*0.3+E{r}*100*0.3,2)"),
				row("ARIRANG 고배당주", "FnGuide", 0.0023, 0.0021, 0.061, 5200000000, "=ROUND((1-C{r}*100)*0.4+(1-D{r}*100)*0.3+E{r}*100*0.3,2)"),
			).
			format(formatPercent, 3, 4, 5).format(formatMoney, 6).format(formatDecimal, 7).
			summary(
				row("최저 보수 ETF", "=INDEX(A{first}:A{last},MATCH(MIN(C{first}:C{last}),C{first}:C{last},0))"),
				row("최고 점수 ETF", "=INDEX(A{first}:A{last},MATCH(MAX(G{first}:G{last}),G{first}:G{last},0))"),
			),
	),
	tmpl("portfolio-risk", "포트폴리오 위험 관리", "주식·투자", "종목별 변동성과 최대손실 한도를 계산해 비중을 점검합니다.",
		sheet("위험관리").tab("#dc4f4f").cols(120, 130, 100, 110, 130, 130, 110).
			title("포트폴리오 위험 관리").note("변동성과 손절폭을 입력하면 예상 최대손실과 위험 기여도가 계산됩니다.").
			head("종목", "평가금액", "비중", "변동성", "손절폭", "예상 최대손실", "위험 기여도").
			rows(
				row("가온전자", 26000000, "=IFERROR(B{r}/SUM(B{first}:B{last}),0)", 0.28, 0.10, "=ROUND(B{r}*E{r},0)", "=IFERROR(C{r}*D{r},0)"),
				row("한빛반도체", 18000000, "=IFERROR(B{r}/SUM(B{first}:B{last}),0)", 0.35, 0.12, "=ROUND(B{r}*E{r},0)", "=IFERROR(C{r}*D{r},0)"),
				row("미래모터스", 14000000, "=IFERROR(B{r}/SUM(B{first}:B{last}),0)", 0.22, 0.08, "=ROUND(B{r}*E{r},0)", "=IFERROR(C{r}*D{r},0)"),
				row("채권 ETF", 22000000, "=IFERROR(B{r}/SUM(B{first}:B{last}),0)", 0.05, 0.03, "=ROUND(B{r}*E{r},0)", "=IFERROR(C{r}*D{r},0)"),
			).
			total("합계", "=SUM(B{first}:B{last})", 1, nil, nil, "=SUM(F{first}:F{last})", "=SUM(G{first}:G{last})").
			format(formatMoney, 2, 6).format(formatPercent, 3, 4, 5, 7).
			summary(
				row("최대 위험 기여 종목", "=INDEX(A{first}:A{last},MATCH(MAX(G{first}:G{last}),G{first}:G{last},0))"),
				row("포트폴리오 손실 한도", won("=SUM(F{first}:F{last})")),
				row("자산 대비 손실 한도", pct("=IFERROR(SUM(F{first}:F{last})/SUM(B{first}:B{last}),0)")),
			),
	),

	// 신용평가 ---------------------------------------------------------------
	tmpl("credit-scorecard", "개인 신용평가 스코어카드", "신용평가", "항목별 배점과 가중치로 신용점수와 등급을 산출합니다.",
		sheet("스코어카드").tab("#9333ea").cols(150, 100, 90, 100, 110, 200).
			title("개인 신용평가 스코어카드").note("각 항목을 0~100점으로 평가하면 가중 점수와 등급이 계산됩니다.").
			head("평가 항목", "가중치", "점수", "가중점수", "구간", "판단 기준").
			rows(
				row("상환 이력", 0.35, 92, "=ROUND(B{r}*C{r},1)", "=IF(C{r}>=90,\"우수\",IF(C{r}>=70,\"보통\",\"주의\"))", "연체 이력과 상환 지연 일수"),
				row("부채 수준", 0.25, 74, "=ROUND(B{r}*C{r},1)", "=IF(C{r}>=90,\"우수\",IF(C{r}>=70,\"보통\",\"주의\"))", "총부채 대비 소득 비율"),
				row("거래 기간", 0.15, 88, "=ROUND(B{r}*C{r},1)", "=IF(C{r}>=90,\"우수\",IF(C{r}>=70,\"보통\",\"주의\"))", "최초 거래 이후 경과 기간"),
				row("신규 신용", 0.10, 65, "=ROUND(B{r}*C{r},1)", "=IF(C{r}>=90,\"우수\",IF(C{r}>=70,\"보통\",\"주의\"))", "최근 6개월 신규 개설 건수"),
				row("신용 형태", 0.15, 80, "=ROUND(B{r}*C{r},1)", "=IF(C{r}>=90,\"우수\",IF(C{r}>=70,\"보통\",\"주의\"))", "카드·대출 구성의 다양성"),
			).
			total("합계", "=SUM(B{first}:B{last})", nil, "=ROUND(SUM(D{first}:D{last}),1)", nil, nil).
			format(formatPercent, 2).format(formatNumber, 3).format(formatDecimal, 4).
			summary(
				row("종합 점수", dec("=ROUND(SUM(D{first}:D{last}),1)")),
				row("신용 등급", "=IF(SUM(D{first}:D{last})>=90,\"1등급\",IF(SUM(D{first}:D{last})>=80,\"2등급\",IF(SUM(D{first}:D{last})>=70,\"3등급\",IF(SUM(D{first}:D{last})>=60,\"4등급\",\"5등급 이하\"))))"),
				row("주의 항목 수", num("=COUNTIF(E{first}:E{last},\"주의\")")),
			),
	),
	tmpl("corporate-credit-rating", "기업 신용평가", "신용평가", "재무비율에 배점을 매겨 기업 신용등급을 산출합니다.",
		sheet("기업평가").tab("#9333ea").cols(160, 120, 100, 90, 100, 110, 110).
			title("기업 신용평가").note("실적 값과 기준값을 비교해 항목 점수를 매기고 가중 합계로 등급을 정합니다.").
			head("평가 지표", "실적", "기준", "판정", "점수", "가중치", "가중점수").
			rows(
				row("부채비율", 1.42, 2.0, "=IF(B{r}<=C{r},\"충족\",\"미달\")", "=IF(B{r}<=C{r},100,60)", 0.25, "=ROUND(E{r}*F{r},1)"),
				row("유동비율", 1.35, 1.2, "=IF(B{r}>=C{r},\"충족\",\"미달\")", "=IF(B{r}>=C{r},100,60)", 0.15, "=ROUND(E{r}*F{r},1)"),
				row("이자보상배율", 3.8, 2.0, "=IF(B{r}>=C{r},\"충족\",\"미달\")", "=IF(B{r}>=C{r},100,50)", 0.25, "=ROUND(E{r}*F{r},1)"),
				row("영업이익률", 0.082, 0.05, "=IF(B{r}>=C{r},\"충족\",\"미달\")", "=IF(B{r}>=C{r},100,60)", 0.20, "=ROUND(E{r}*F{r},1)"),
				row("매출 성장률", 0.031, 0.05, "=IF(B{r}>=C{r},\"충족\",\"미달\")", "=IF(B{r}>=C{r},100,70)", 0.15, "=ROUND(E{r}*F{r},1)"),
			).
			total("합계", nil, nil, nil, nil, "=SUM(F{first}:F{last})", "=ROUND(SUM(G{first}:G{last}),1)").
			format(formatDecimal, 2, 3, 7).format(formatNumber, 5).format(formatPercent, 6).
			summary(
				row("종합 점수", dec("=ROUND(SUM(G{first}:G{last}),1)")),
				row("신용 등급", "=IF(SUM(G{first}:G{last})>=95,\"AAA\",IF(SUM(G{first}:G{last})>=88,\"AA\",IF(SUM(G{first}:G{last})>=80,\"A\",IF(SUM(G{first}:G{last})>=70,\"BBB\",\"BB 이하\"))))"),
				row("미달 지표", num("=COUNTIF(D{first}:D{last},\"미달\")")),
			),
	),
	tmpl("loan-application-review", "여신 심사표", "신용평가", "LTV·DTI·DSR을 계산해 대출 승인 가능 여부를 판단합니다.",
		sheet("여신심사").tab("#dc4f4f").cols(180, 160, 120, 120, 190).
			title("여신 심사표").note("소득과 담보가액, 기존 부채를 입력하면 규제 비율과 판정이 계산됩니다.").
			head("항목", "값", "규제 한도", "판정", "산식").
			rows(
				row("연소득", 78000000, nil, nil, "세전 기준"),
				row("담보 시세", 620000000, nil, nil, "KB 시세"),
				row("신청 대출금", 340000000, nil, nil, "주택담보대출"),
				row("기존 대출 잔액", 45000000, nil, nil, "신용대출 포함"),
				row("연 원리금 상환액", "=ROUND(B6*(0.045/12)/(1-POWER(1+0.045/12,-360))*12,0)", nil, nil, "30년 원리금 균등"),
				row("기존 연 상환액", 7200000, nil, nil, "기존 대출 기준"),
				row("LTV", "=IFERROR(B6/B5,0)", 0.70, "=IF(B10<=C10,\"충족\",\"초과\")", "대출금 / 담보 시세"),
				row("DTI", "=IFERROR(B8/B4,0)", 0.60, "=IF(B11<=C11,\"충족\",\"초과\")", "원리금 / 연소득"),
				row("DSR", "=IFERROR((B8+B9)/B4,0)", 0.40, "=IF(B12<=C12,\"충족\",\"초과\")", "총 상환액 / 연소득"),
			).
			format(formatMoney, 2).format(formatPercent, 3).
			summary(
				row("승인 판정", "=IF(COUNTIF(D10:D12,\"초과\")=0,\"승인 가능\",\"조건 조정 필요\")"),
				row("초과 항목 수", num("=COUNTIF(D10:D12,\"초과\")")),
				row("DSR 여유 한도", won("=ROUND(MAX(B4*0.4-(B8+B9),0),0)")),
			),
	),
	tmpl("delinquency-aging", "연체 채권 연령분석", "신용평가", "연체 기간별 잔액과 대손충당금을 계산합니다.",
		sheet("연령분석").tab("#dc4f4f").cols(130, 110, 130, 100, 110, 130, 110).
			title("연체 채권 연령분석").note("연체 일수 구간별 잔액에 충당률을 적용해 대손충당금을 산출합니다.").
			head("거래처", "연체 일수", "채권 잔액", "구간", "충당률", "대손충당금", "위험도").
			rows(
				row("가온테크", 15, 12000000, "=IF(B{r}<=30,\"30일 이하\",IF(B{r}<=90,\"31~90일\",IF(B{r}<=180,\"91~180일\",\"180일 초과\")))", "=IF(B{r}<=30,0.01,IF(B{r}<=90,0.1,IF(B{r}<=180,0.3,0.5)))", "=ROUND(C{r}*E{r},0)", "=IF(E{r}>=0.3,\"높음\",IF(E{r}>=0.1,\"중간\",\"낮음\"))"),
				row("한빛물류", 62, 8400000, "=IF(B{r}<=30,\"30일 이하\",IF(B{r}<=90,\"31~90일\",IF(B{r}<=180,\"91~180일\",\"180일 초과\")))", "=IF(B{r}<=30,0.01,IF(B{r}<=90,0.1,IF(B{r}<=180,0.3,0.5)))", "=ROUND(C{r}*E{r},0)", "=IF(E{r}>=0.3,\"높음\",IF(E{r}>=0.1,\"중간\",\"낮음\"))"),
				row("미래에너지", 128, 21500000, "=IF(B{r}<=30,\"30일 이하\",IF(B{r}<=90,\"31~90일\",IF(B{r}<=180,\"91~180일\",\"180일 초과\")))", "=IF(B{r}<=30,0.01,IF(B{r}<=90,0.1,IF(B{r}<=180,0.3,0.5)))", "=ROUND(C{r}*E{r},0)", "=IF(E{r}>=0.3,\"높음\",IF(E{r}>=0.1,\"중간\",\"낮음\"))"),
				row("성진산업", 240, 6300000, "=IF(B{r}<=30,\"30일 이하\",IF(B{r}<=90,\"31~90일\",IF(B{r}<=180,\"91~180일\",\"180일 초과\")))", "=IF(B{r}<=30,0.01,IF(B{r}<=90,0.1,IF(B{r}<=180,0.3,0.5)))", "=ROUND(C{r}*E{r},0)", "=IF(E{r}>=0.3,\"높음\",IF(E{r}>=0.1,\"중간\",\"낮음\"))"),
				row("대현식품", 8, 15200000, "=IF(B{r}<=30,\"30일 이하\",IF(B{r}<=90,\"31~90일\",IF(B{r}<=180,\"91~180일\",\"180일 초과\")))", "=IF(B{r}<=30,0.01,IF(B{r}<=90,0.1,IF(B{r}<=180,0.3,0.5)))", "=ROUND(C{r}*E{r},0)", "=IF(E{r}>=0.3,\"높음\",IF(E{r}>=0.1,\"중간\",\"낮음\"))"),
			).
			total("합계", nil, "=SUM(C{first}:C{last})", nil, nil, "=SUM(F{first}:F{last})", nil).
			format(formatNumber, 2).format(formatMoney, 3, 6).format(formatPercent, 5).
			summary(
				row("충당금 비율", pct("=IFERROR(SUM(F{first}:F{last})/SUM(C{first}:C{last}),0)")),
				row("90일 초과 잔액", won("=SUMIF(B{first}:B{last},\">90\",C{first}:C{last})")),
				row("고위험 거래처", num("=COUNTIF(G{first}:G{last},\"높음\")")),
			),
	),
	tmpl("counterparty-limit", "거래처 여신한도 관리", "신용평가", "등급별 한도와 사용액을 비교해 한도 초과를 감시합니다.",
		sheet("여신한도").tab("#f59e0b").cols(150, 90, 130, 130, 130, 100, 100).
			title("거래처 여신한도 관리").note("신용등급에 따른 한도와 현재 사용액을 비교해 잔여 한도를 관리합니다.").
			head("거래처", "등급", "여신한도", "사용액", "잔여 한도", "사용률", "상태").
			rows(
				row("가온테크", "A", 200000000, 142000000, "=C{r}-D{r}", "=IFERROR(D{r}/C{r},0)", "=IF(D{r}>C{r},\"초과\",IF(F{r}>=0.8,\"주의\",\"정상\"))"),
				row("한빛물류", "B", 120000000, 118000000, "=C{r}-D{r}", "=IFERROR(D{r}/C{r},0)", "=IF(D{r}>C{r},\"초과\",IF(F{r}>=0.8,\"주의\",\"정상\"))"),
				row("미래에너지", "C", 60000000, 71000000, "=C{r}-D{r}", "=IFERROR(D{r}/C{r},0)", "=IF(D{r}>C{r},\"초과\",IF(F{r}>=0.8,\"주의\",\"정상\"))"),
				row("성진산업", "A", 250000000, 96000000, "=C{r}-D{r}", "=IFERROR(D{r}/C{r},0)", "=IF(D{r}>C{r},\"초과\",IF(F{r}>=0.8,\"주의\",\"정상\"))"),
				row("대현식품", "B", 100000000, 34000000, "=C{r}-D{r}", "=IFERROR(D{r}/C{r},0)", "=IF(D{r}>C{r},\"초과\",IF(F{r}>=0.8,\"주의\",\"정상\"))"),
			).
			total("합계", nil, "=SUM(C{first}:C{last})", "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", "=IFERROR(SUM(D{first}:D{last})/SUM(C{first}:C{last}),0)", nil).
			format(formatMoney, 3, 4, 5).format(formatPercent, 6).
			summary(
				row("한도 초과 거래처", num("=COUNTIF(G{first}:G{last},\"초과\")")),
				row("A등급 여신 잔액", won("=SUMIF(B{first}:B{last},\"A\",D{first}:D{last})")),
			),
	),
	tmpl("financial-ratios", "재무비율 분석", "신용평가", "재무제표 항목에서 안정성·수익성·활동성 비율을 계산합니다.",
		sheet("재무비율").tab("#0f766e").cols(170, 150, 150, 120, 110).
			title("재무비율 분석").note("당기와 전기 금액을 입력하면 주요 비율과 증감이 계산됩니다.").
			head("항목", "당기", "전기", "증감", "증감율").
			rows(
				row("매출액", 48500000000, 42000000000, "=B{r}-C{r}", "=IFERROR(D{r}/C{r},0)"),
				row("영업이익", 3980000000, 3120000000, "=B{r}-C{r}", "=IFERROR(D{r}/C{r},0)"),
				row("당기순이익", 2760000000, 2210000000, "=B{r}-C{r}", "=IFERROR(D{r}/C{r},0)"),
				row("유동자산", 18600000000, 16400000000, "=B{r}-C{r}", "=IFERROR(D{r}/C{r},0)"),
				row("유동부채", 13800000000, 13100000000, "=B{r}-C{r}", "=IFERROR(D{r}/C{r},0)"),
				row("총자산", 52000000000, 48700000000, "=B{r}-C{r}", "=IFERROR(D{r}/C{r},0)"),
				row("총부채", 30500000000, 29800000000, "=B{r}-C{r}", "=IFERROR(D{r}/C{r},0)"),
				row("자기자본", "=B9-B10", "=C9-C10", "=B{r}-C{r}", "=IFERROR(D{r}/C{r},0)"),
			).
			format(formatMoney, 2, 3, 4).format(formatPercent, 5).
			summary(
				row("유동비율", pct("=IFERROR(B7/B8,0)")),
				row("부채비율", pct("=IFERROR(B10/B11,0)")),
				row("영업이익률", pct("=IFERROR(B5/B4,0)")),
				row("ROE", pct("=IFERROR(B6/B11,0)")),
				row("총자산회전율", dec("=IFERROR(ROUND(B4/B9,2),0)")),
			),
	),

	// 금융 -------------------------------------------------------------------
	tmpl("loan-comparison", "대출 상품 비교", "금융", "금리와 기간이 다른 대출을 월 상환액과 총이자로 비교합니다.",
		sheet("대출비교").tab("#5268a6").cols(150, 130, 90, 90, 130, 140, 130).
			title("대출 상품 비교").note("대출금과 연이율, 기간을 입력하면 원리금 균등 상환 기준으로 계산됩니다.").
			head("상품", "대출금", "연이율", "기간(개월)", "월 상환액", "총 상환액", "총 이자").
			rows(
				row("가온은행 주담대", 300000000, 0.041, 360, "=ROUND(B{r}*(C{r}/12)/(1-POWER(1+C{r}/12,-D{r})),0)", "=E{r}*D{r}", "=F{r}-B{r}"),
				row("한빛은행 주담대", 300000000, 0.038, 240, "=ROUND(B{r}*(C{r}/12)/(1-POWER(1+C{r}/12,-D{r})),0)", "=E{r}*D{r}", "=F{r}-B{r}"),
				row("미래캐피탈 신용", 50000000, 0.079, 60, "=ROUND(B{r}*(C{r}/12)/(1-POWER(1+C{r}/12,-D{r})),0)", "=E{r}*D{r}", "=F{r}-B{r}"),
				row("성진저축 사업자", 120000000, 0.062, 120, "=ROUND(B{r}*(C{r}/12)/(1-POWER(1+C{r}/12,-D{r})),0)", "=E{r}*D{r}", "=F{r}-B{r}"),
			).
			format(formatMoney, 2, 5, 6, 7).format(formatPercent, 3).format(formatNumber, 4).
			summary(
				row("총이자 최저 상품", "=INDEX(A{first}:A{last},MATCH(MIN(G{first}:G{last}),G{first}:G{last},0))"),
				row("월 상환 최저 상품", "=INDEX(A{first}:A{last},MATCH(MIN(E{first}:E{last}),E{first}:E{last},0))"),
			),
	),
	tmpl("savings-goal", "목표 저축 계획", "금융", "목표 금액에 도달하기까지 필요한 월 저축액과 복리 효과를 계산합니다.",
		sheet("저축계획").tab("#22c55e").cols(170, 150, 200).
			title("목표 저축 계획").note("목표 금액과 기간, 이자율을 넣으면 필요한 월 저축액이 계산됩니다.").
			head("항목", "값", "설명").
			rows(
				row("목표 금액", 100000000, "달성하려는 금액"),
				row("현재 저축액", 12000000, "이미 모은 금액"),
				row("목표 기간(년)", 5, "달성까지 남은 기간"),
				row("연 이자율", 0.035, "세전 기준"),
				row("현재 저축액의 미래가치", "=ROUND(B5*POWER(1+B7,B6),0)", "복리 적용"),
				row("추가로 모아야 할 금액", "=MAX(B4-B8,0)", "목표 - 현재 저축 미래가치"),
				row("월 필요 저축액", "=ROUND(B9/(B6*12),0)", "이자를 뺀 단순 계산"),
				row("이자 고려 월 저축액", "=ROUND(B9*(B7/12)/(POWER(1+B7/12,B6*12)-1),0)", "적립식 복리 기준"),
			).
			format(formatMoney, 2).
			summary(
				row("절감되는 월 부담", won("=MAX(B10-B11,0)")),
				row("총 이자 수익", won("=ROUND(B11*B6*12-B9,0)")),
			),
	),
	tmpl("retirement-plan", "은퇴 자금 시뮬레이션", "금융", "은퇴 시점의 자산과 은퇴 후 인출 가능 금액을 추정합니다.",
		sheet("은퇴계획").tab("#22c55e").cols(170, 150, 210).
			title("은퇴 자금 시뮬레이션").note("현재 자산과 적립액, 수익률을 넣으면 은퇴 시점 자산이 계산됩니다.").
			head("항목", "값", "설명").
			rows(
				row("현재 나이", 38, "만 나이"),
				row("은퇴 예정 나이", 60, "목표"),
				row("남은 기간(년)", "=B5-B4", "은퇴까지"),
				row("현재 은퇴자산", 85000000, "연금·투자 합계"),
				row("연간 적립액", 9600000, "월 80만원"),
				row("기대 수익률", 0.055, "세후 연 수익률"),
				row("현재 자산의 미래가치", "=ROUND(B7*POWER(1+B9,B6),0)", "복리"),
				row("적립액의 미래가치", "=ROUND(B8*(POWER(1+B9,B6)-1)/B9,0)", "기말 적립 기준"),
				row("은퇴 시점 총자산", "=B10+B11", "합계"),
				row("은퇴 후 기간(년)", 25, "기대 수명까지"),
				row("연간 인출 가능액", "=ROUND(B12/B13,0)", "수익 없이 균등 인출"),
			).
			format(formatMoney, 2).
			summary(
				row("월 인출 가능액", won("=ROUND(B14/12,0)")),
				row("목표 대비 달성률", pct("=IFERROR(B12/500000000,0)")),
			),
	),
	tmpl("cash-budget-12m", "12개월 자금수지 계획", "금융", "월별 수입과 지출로 자금 잔액과 부족 시점을 예측합니다.",
		sheet("자금수지").tab("#0f766e").cols(90, 130, 130, 130, 140, 100).
			title("12개월 자금수지 계획").note("월별 수입과 지출을 넣으면 잔액이 이어지고 부족한 달을 표시합니다.").
			head("월", "수입", "지출", "월 수지", "누적 잔액", "상태").
			rows(
				row("1월", 42000000, 38500000, "=B{r}-C{r}", "=30000000+D{r}", "=IF(E{r}<0,\"부족\",IF(E{r}<10000000,\"주의\",\"양호\"))"),
				row("2월", 39500000, 41200000, "=B{r}-C{r}", "=E{p}+D{r}", "=IF(E{r}<0,\"부족\",IF(E{r}<10000000,\"주의\",\"양호\"))"),
				row("3월", 45800000, 43100000, "=B{r}-C{r}", "=E{p}+D{r}", "=IF(E{r}<0,\"부족\",IF(E{r}<10000000,\"주의\",\"양호\"))"),
				row("4월", 41200000, 47600000, "=B{r}-C{r}", "=E{p}+D{r}", "=IF(E{r}<0,\"부족\",IF(E{r}<10000000,\"주의\",\"양호\"))"),
				row("5월", 48900000, 42300000, "=B{r}-C{r}", "=E{p}+D{r}", "=IF(E{r}<0,\"부족\",IF(E{r}<10000000,\"주의\",\"양호\"))"),
				row("6월", 52100000, 45800000, "=B{r}-C{r}", "=E{p}+D{r}", "=IF(E{r}<0,\"부족\",IF(E{r}<10000000,\"주의\",\"양호\"))"),
			).
			total("합계", "=SUM(B{first}:B{last})", "=SUM(C{first}:C{last})", "=SUM(D{first}:D{last})", "=E{last}", nil).
			format(formatMoney, 2, 3, 4, 5).
			summary(
				row("최저 잔액", won("=MIN(E{first}:E{last})")),
				row("부족 예상 월", num("=COUNTIF(F{first}:F{last},\"부족\")")),
				row("평균 월 수지", won("=ROUND(AVERAGE(D{first}:D{last}),0)")),
			),
	),
	tmpl("fx-exposure", "외화 자산 · 환위험 관리", "금융", "통화별 외화 자산을 원화로 환산하고 환율 변동 영향을 봅니다.",
		sheet("환위험").tab("#3b82f6").cols(90, 130, 110, 110, 150, 150, 130).
			title("외화 자산과 환위험").note("보유 외화와 장부 환율, 현재 환율을 입력하면 환산손익이 계산됩니다.").
			head("통화", "보유 금액", "장부 환율", "현재 환율", "장부가(원)", "평가액(원)", "환산손익").
			rows(
				row("USD", 250000, 1320.5, 1385.2, "=ROUND(B{r}*C{r},0)", "=ROUND(B{r}*D{r},0)", "=F{r}-E{r}"),
				row("EUR", 80000, 1445.0, 1502.8, "=ROUND(B{r}*C{r},0)", "=ROUND(B{r}*D{r},0)", "=F{r}-E{r}"),
				row("JPY", 12000000, 8.95, 9.24, "=ROUND(B{r}*C{r},0)", "=ROUND(B{r}*D{r},0)", "=F{r}-E{r}"),
				row("CNY", 900000, 182.3, 189.6, "=ROUND(B{r}*C{r},0)", "=ROUND(B{r}*D{r},0)", "=F{r}-E{r}"),
			).
			total("합계", nil, nil, nil, "=SUM(E{first}:E{last})", "=SUM(F{first}:F{last})", "=SUM(G{first}:G{last})").
			format(formatNumber, 2).format(formatDecimal, 3, 4).format(formatMoney, 5, 6, 7).
			summary(
				row("환산손익률", pct("=IFERROR(SUM(G{first}:G{last})/SUM(E{first}:E{last}),0)")),
				row("최대 노출 통화", "=INDEX(A{first}:A{last},MATCH(MAX(F{first}:F{last}),F{first}:F{last},0))"),
				row("환율 1% 변동 영향", won("=ROUND(SUM(F{first}:F{last})*0.01,0)")),
			),
	),
	tmpl("insurance-portfolio", "보험 가입 현황", "금융", "보장 내용과 보험료를 정리해 중복과 공백을 점검합니다.",
		sheet("보험").tab("#ec4899").cols(140, 120, 110, 130, 150, 110, 110).
			title("보험 가입 현황").note("월 보험료와 보장 금액을 입력하면 연 보험료와 보장 대비 비용이 계산됩니다.").
			head("보험사", "상품 유형", "피보험자", "월 보험료", "보장 금액", "연 보험료", "보장 배수").
			rows(
				row("가온생명", "종신", "본인", 185000, 200000000, "=D{r}*12", "=IFERROR(ROUND(E{r}/F{r},1),0)"),
				row("한빛손보", "실손", "본인", 42000, 50000000, "=D{r}*12", "=IFERROR(ROUND(E{r}/F{r},1),0)"),
				row("미래화재", "암보험", "배우자", 68000, 100000000, "=D{r}*12", "=IFERROR(ROUND(E{r}/F{r},1),0)"),
				row("성진생명", "연금", "본인", 300000, 0, "=D{r}*12", "=IFERROR(ROUND(E{r}/F{r},1),0)"),
				row("한빛손보", "자동차", "본인", 95000, 300000000, "=D{r}*12", "=IFERROR(ROUND(E{r}/F{r},1),0)"),
			).
			total("합계", nil, nil, "=SUM(D{first}:D{last})", "=SUM(E{first}:E{last})", "=SUM(F{first}:F{last})", nil).
			format(formatMoney, 4, 5, 6).format(formatDecimal, 7).
			summary(
				row("연 보험료 합계", won("=SUM(F{first}:F{last})")),
				row("본인 월 보험료", won("=SUMIF(C{first}:C{last},\"본인\",D{first}:D{last})")),
				row("보험 상품 수", num("=COUNTA(A{first}:A{last})")),
			),
	),
}
