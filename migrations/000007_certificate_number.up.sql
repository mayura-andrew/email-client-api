-- Printed on the certificate; comes from the CSV and may be empty.
ALTER TABLE campaign_recipients ADD COLUMN certificate_number VARCHAR(128) NOT NULL DEFAULT '';
