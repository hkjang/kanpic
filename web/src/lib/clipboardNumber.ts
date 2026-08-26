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

import { significantDigits } from './spreadsheetNumber'

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
  // 배정밀도가 정확히 담는 것은 열다섯 자리까지다. 그보다 긴 것은 계좌번호나
  // 전표번호이지 금액이 아니다. 숫자로 바꾸면 뒤가 뭉개져, 붙여넣은 사람이
  // 보던 것과 다른 값이 칸에 들어간다. 글자로 두면 적어도 그대로 남는다.
  if(significantDigits(digits)>15)return
  const parsed=Number(digits)
  if(!Number.isFinite(parsed))return
  const value=(negative?-parsed:parsed)/(percent?100:1)
  // 서식이 필요 없는 평범한 숫자는 서식을 붙이지 않는다. 붙이면 셀마다
  // 쓸모없는 스타일이 하나씩 생긴다.
  const parenthesized=negative&&text.startsWith('(')&&text.endsWith(')')
  if(!grouped&&currency===''&&suffix===''&&!percent&&!parenthesized)return {value}
  return {value,numberFormat:pastedNumberFormat({grouped,currency,suffix,percent,parenthesized,decimals:fraction?.length??0})}
}

/**
 * 보이던 모습을 되살리는 표시 형식. spreadsheetNumber.ts 의
 * numberFormatForText 와 같은 글자를 내야 한다. 같은 "₩5,000" 이 붙여넣기로
 * 들어올 때와 데이터 정리로 고쳐질 때 서식이 다르면, 같은 칸이 어떻게
 * 들어왔는지에 따라 다르게 저장된다.
 */
function pastedNumberFormat({grouped,currency,suffix,percent,parenthesized,decimals}:{grouped:boolean;currency:string;suffix:string;percent:boolean;parenthesized:boolean;decimals:number}){
  const body=(currency!==''?`"${currency}"`:'')+(grouped?'#,##0':'0')+(decimals>0?'.'+'0'.repeat(decimals):'')+(percent?'%':'')+(suffix!==''?`"${suffix}"`:'')
  // 회계 표기의 괄호는 음수를 적는 꼴이다. 떼어 버리면 (500) 이 -500 으로
  // 보이고, 사람은 자기가 옮긴 표와 다른 것을 보게 된다.
  return parenthesized?`${body};(${body})`:body
}
