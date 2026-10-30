import http from 'k6/http';
import { check } from 'k6';

export const options = {
  scenarios: {
    product_card: {
      executor: 'constant-arrival-rate',
      rate: 3000,
      timeUnit: '1s',
      duration: __ENV.DURATION || '30s',
      preAllocatedVUs: 128,
      maxVUs: 512,
    },
  },
  summaryTrendStats: ['avg', 'p(50)', 'p(95)', 'p(99)', 'max'],
  thresholds: {
    http_req_duration: ['p(99)<80'],
    http_req_failed: ['rate<0.01'],
    checks: ['rate>0.99'],
    dropped_iterations: ['count==0'],
  },
};

export default function () {
  const response = http.get(`${__ENV.BASE_URL || 'http://localhost:8080'}/v1/products/product-01`);
  check(response, { 'status 200': (r) => r.status === 200 });
}
