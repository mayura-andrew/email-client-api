# Send Bulk Email API using GO 

![Email API](https://github.com/mayura-andrew/send-bulk-email-client-api/assets/48531182/243b444d-9284-4008-be17-f678fe602c75)


This API is developed for the ScholarX platform to handle the sending, and querying of emails. It provides several endpoints for these operations. Visit here [https://sefglobal.org/].

Running API URL := (https://64.227.135.79/)
## Endpoints

### POST /send (Status : Completed ☑️)

This endpoint is used to send an email. The request body should be a JSON object with the following fields:

- `recipient`: The email address of the recipient.
- `subject`: The subject of the email.
- `body`: The body of the email.
  
```
{
    "sender": "",
    "recipients": [],
    "subject": "example",
    "body": "example"
}
```

### POST /track  (Status : Completed ☑️)

This endpoint is used to update the tracking status of an email. The request body should be a JSON object with the following fields:

- `id`: The ID of the email to track.
- `opened`: A boolean indicating whether the email has been opened.
## How it works 

![image](https://github.com/mayura-andrew/send-bulk-email-client-api/assets/48531182/2c5f7568-97d3-46e3-8645-35663a5b43db)


### GET /successfullysent (Status : Not Started ❌)

This endpoint retrieves all emails that have been successfully sent. It returns a JSON array of email objects.

### GET /notsuccessfullysent  (Status : Not Started ❌)

This endpoint retrieves all emails that have not been successfully sent. It returns a JSON array of email objects.

### GET /totalcount  (Status : Not Started ❌)

This endpoint retrieves the total count of emails. It returns a JSON object with a single field, `count`, containing the count.

### GET /search  (Status : Not Started ❌)

This endpoint searches for emails based on certain criteria. It accepts the following query parameters:

- `recipient`: The email address to search for.
- `subject`: The subject to search for.
- `sent`: A boolean indicating whether to search for emails that have been sent (`true`) or not sent (`false`).
- `opened`: A boolean indicating whether to search for emails that have been opened (`true`) or not opened (`false`).

## Email Template  (Status : Completed ☑️)

The content of the emails is generated from a Go template file, `email_template.tmpl`. This file defines two templates, `subject` and `plainBody`, which are used to generate the subject and body of the email, respectively. The templates have access to the following data:

- `Subject`: The subject of the email.
- `Body`: The body of the email.
- `Recipient`: The email address of the recipient.

## Docker Usage

This application is also available as a Docker image and can be pulled from Docker Hub and run locally. 


https://hub.docker.com/r/mayuraandrew/go-email-client

---

Thank you 😄
