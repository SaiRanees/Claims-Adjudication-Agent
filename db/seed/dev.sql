-- Synthetic development data. Nothing here is a real person, policy or claim.
--
-- One individual Arogya Sanjeevani policy on the Go Digit wording (UIN GODHLIP24179V022324):
-- the 2026-27 policy year with a sum insured of ₹5,00,000, and one insured member who has
-- been covered continuously since 1 April 2023.
--
-- Safe to re-run: existing rows are left alone. `go run ./cmd/dev reset` deletes all local data.

BEGIN;

INSERT INTO policies (id, insurer_code, policy_number, product_code, wording_uin,
                      cover_type, first_inception_date)
VALUES ('01930000-0000-7000-8000-000000000001', 'GODIGIT', 'SYN-AS-000001',
        'arogya-sanjeevani', 'GODHLIP24179V022324', 'individual', DATE '2023-04-01')
ON CONFLICT (id) DO NOTHING;

INSERT INTO policy_periods (id, policy_id, starts_on, ends_on,
                            sum_insured_paise, cumulative_bonus_paise)
VALUES ('01930000-0000-7000-8000-000000000101', '01930000-0000-7000-8000-000000000001',
        DATE '2026-04-01', DATE '2027-03-31', 50000000, 0)
ON CONFLICT (id) DO NOTHING;

INSERT INTO members (id, policy_id, member_code, full_name, date_of_birth,
                     relationship, covered_since)
VALUES ('01930000-0000-7000-8000-000000000201', '01930000-0000-7000-8000-000000000001',
        'M01', 'Synthetic Member One', DATE '1986-07-14', 'self', DATE '2023-04-01')
ON CONFLICT (id) DO NOTHING;

COMMIT;
