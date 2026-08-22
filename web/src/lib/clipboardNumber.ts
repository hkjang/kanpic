/**
 * 스프레드시트 사이를 오가는 값은 계산된 숫자가 아니라 **보이던 글자**로
 * 건너온다. 엑셀에서 통화 서식이 붙은 열을 복사하면 클립보드의 평문에는
 * `₩1,234.50` 이 들어 있고, 이것을 글자로 저장하면 합계가 0이 된다.
 */

/** 자릿수 구분 쉼표는 정확히 세 자리씩 끊을 때만 구분자로 인정한다.
 *  그렇지 않으면 소수점으로 쉼표를 쓰는 표기(`1,2`)가 12가 되어 버린다. */
const GROUPED=/^\d{1,3}(,\d{3})+$/
const PLAIN=/^\d+$/
const CURRENCY='₩$€£¥¢'

export type ParsedNumber={value:number;numberFormat?:string}

/**
 * 붙여넣은 글자가 스프레드시트가 숫자로 다루었을 값이면 그 숫자와, 보이던
 * 모습을 되살리는 표시 형식을 함께 돌려준다. 숫자가 아니면 undefined.
 */
export function parsePastedNumber(raw:string):ParsedNumber|undefined{
  const text=raw.trim()
  if(text===''||text.length>40)return
  let body=text
  let negative=false
  // 회계 표기의 괄호 음수: (1,234)
  if(body.startsWith('(')&&body.endsWith(')')){
    negative=true
    body=body.slice(1,-1).trim()
  }
  if(body.startsWith('-')||body.startsWith('+')){
    negative=negative!==body.startsWith('-')
    body=body.slice(1).trim()
  }
  let currency=''
  let suffix=''
  if(body.length>0&&CURRENCY.includes(body[0])){
    currency=body[0]
    body=body.slice(1).trim()
  }else if(body.endsWith('원')){
    suffix='원'
    body=body.slice(0,-1).trim()
  }
  let percent=false
  if(body.endsWith('%')){
    percent=true
    body=body.slice(0,-1).trim()
  }
  if(currency!==''&&percent)return
  const [integer,fraction,...rest]=body.split('.')
  if(rest.length>0||fraction!==undefined&&!PLAIN.test(fraction))return
  const grouped=GROUPED.test(integer)
  if(!grouped&&!PLAIN.test(integer))return
  const digits=integer.replace(/,/g,'')+(fraction===undefined?'':'.'+fraction)
  const parsed=Number(digits)
  if(!Number.isFinite(parsed))return
  const value=(negative?-parsed:parsed)/(percent?100:1)
  // 서식이 필요 없는 평범한 숫자는 서식을 붙이지 않는다. 붙이면 셀마다
  // 쓸모없는 스타일이 하나씩 생긴다.
  if(!grouped&&currency===''&&suffix===''&&!percent)return {value}
  return {value,numberFormat:pastedNumberFormat({grouped,currency,suffix,percent,decimals:fraction?.length??0})}
}

function pastedNumberFormat({grouped,currency,suffix,percent,decimals}:{grouped:boolean;currency:string;suffix:string;percent:boolean;decimals:number}){
  const body=(grouped?'#,##0':'0')+(decimals>0?'.'+'0'.repeat(decimals):'')
  if(percent)return body+'%'
  if(currency!=='')return `"${currency}"`+body
  if(suffix!=='')return body+`"${suffix}"`
  return body
}
